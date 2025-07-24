// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"golang.org/x/perf/benchfmt"
	"golang.org/x/perf/benchmath"
	"golang.org/x/perf/benchproc"
	"golang.org/x/perf/benchunit"
)

type Config struct {
	Workload    string
	Duration    time.Duration
	FlushPeriod time.Duration
}

func (c Config) String() string {
	ratio := float64(c.FlushPeriod) / float64(c.Duration)
	return fmt.Sprintf("%s %v (%.0fx)", c.Workload, c.Duration, ratio)
}

func (c Config) IsValidLatency(latency time.Duration) bool {
	// Per BENCHMARKING.md: p99 latency should be less than flush period + task duration
	threshold := c.FlushPeriod + c.Duration
	return latency < threshold
}

type BenchData struct {
	Config            Config
	CombinerLimit     int
	ThroughputValues  []float64         // raw values collected before creating sample
	P99LatencyValues  []float64         // raw values collected before creating sample
	ThroughputSample  *benchmath.Sample // tasks/sec
	P99LatencySample  *benchmath.Sample // p99-workflow-latency-ns
	ThroughputSummary benchmath.Summary
	P99LatencySummary benchmath.Summary
}

type ComparisonResult struct {
	Config                Config
	BaselineBest          BenchData
	CurrentBest           BenchData
	ThroughputImprovement float64 // percentage change
	LatencyImprovement    float64 // percentage change (negative means worse)
	ThroughputSignificant bool
	LatencySignificant    bool
	BaselineUnlimited     *BenchData
	CurrentUnlimited      *BenchData
	LatencyThresholdDelta float64 // percentage over/under threshold (negative means under/good)

	// Unlimited analysis with significance testing
	UnlimitedThroughputImprovement       float64 // ∞→∞ throughput improvement percentage
	UnlimitedThroughputSignificant       bool    // ∞→∞ throughput significance
	UnlimitedLatencyImprovement          float64 // ∞→∞ latency improvement percentage
	UnlimitedLatencySignificant          bool    // ∞→∞ latency significance
	BaselineBestToUnlimitedThroughput    float64 // B→∞ throughput percentage difference
	BaselineBestToUnlimitedThroughputSig bool    // B→∞ throughput significance
	BaselineBestToUnlimitedLatency       float64 // B→∞ latency percentage difference
	BaselineBestToUnlimitedLatencySig    bool    // B→∞ latency significance
	CurrentBestToUnlimitedThroughput     float64 // C→∞ throughput percentage difference
	CurrentBestToUnlimitedThroughputSig  bool    // C→∞ throughput significance
	CurrentBestToUnlimitedLatency        float64 // C→∞ latency percentage difference
	CurrentBestToUnlimitedLatencySig     bool    // C→∞ latency significance
	ThroughputEfficiencyDelta            float64 // Δ between B→∞ and C→∞ throughput ratios
	LatencyEfficiencyDelta               float64 // Δ between B→∞ and C→∞ latency ratios
}

func main() {
	var baseline = flag.String("baseline", "", "Baseline benchmark file")
	var current = flag.String("current", "", "Current benchmark file")
	flag.Parse()

	if *baseline == "" || *current == "" {
		fmt.Fprintf(os.Stderr, "Usage: %s -baseline <file> -current <file>\n", os.Args[0])
		os.Exit(1)
	}

	baselineData, err := loadBenchmarkData(*baseline)
	if err != nil {
		log.Fatalf("Error loading baseline: %v", err)
	}

	currentData, err := loadBenchmarkData(*current)
	if err != nil {
		log.Fatalf("Error loading current: %v", err)
	}

	fmt.Println()

	// Print gatherOnly results first (combinerLimit=0)
	printGatherOnlyResults(baselineData, currentData)

	results := compareBenchmarks(baselineData, currentData)

	fmt.Println()
	fmt.Println()
	printSummaryResults(results)

	fmt.Println()
	fmt.Println()
	// Print unlimited analysis table
	printUnlimitedAnalysis(results)
	fmt.Println()
}

func loadBenchmarkData(filename string) (map[Config]map[int]*BenchData, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var pp benchproc.ProjectionParser

	// Parse out the key dimensions we care about
	workloadP, err := pp.Parse("/workload", nil)
	if err != nil {
		return nil, err
	}
	durationP, err := pp.Parse("/duration", nil)
	if err != nil {
		return nil, err
	}
	flushPeriodP, err := pp.Parse("/flushPeriod", nil)
	if err != nil {
		return nil, err
	}
	combinerLimitP, err := pp.Parse("/combinerLimit", nil)
	if err != nil {
		return nil, err
	}

	// Get the field references for key extraction
	workloadField := workloadP.FlattenedFields()[0]
	durationField := durationP.FlattenedFields()[0]
	flushPeriodField := flushPeriodP.FlattenedFields()[0]
	combinerLimitField := combinerLimitP.FlattenedFields()[0]

	data := make(map[Config]map[int]*BenchData)

	benchFiles := &benchfmt.Files{
		Paths: []string{filename},
	}

	for benchFiles.Scan() {
		switch rec := benchFiles.Result(); rec := rec.(type) {
		case *benchfmt.Result:
			// Process all benchmarks - combinerLimit=0 is gatherOnly, positive values are combine

			// Extract configuration
			workloadKey := workloadP.Project(rec)
			durationKey := durationP.Project(rec)
			flushPeriodKey := flushPeriodP.Project(rec)
			combinerLimitKey := combinerLimitP.Project(rec)

			config, err := parseConfig(workloadKey.Get(workloadField), durationKey.Get(durationField), flushPeriodKey.Get(flushPeriodField))
			if err != nil {
				log.Printf("Skipping benchmark with invalid config: %v", err)
				continue
			}

			limit, err := parseCombinerLimit(combinerLimitKey.Get(combinerLimitField))
			if err != nil {
				log.Printf("Skipping benchmark with invalid combiner limit: %v", err)
				continue
			}

			// Extract metrics
			var throughput, latency []float64

			// Check rec.Values for both throughput and latency
			for _, v := range rec.Values {
				v.Value, v.Unit = benchunit.Tidy(v.Value, v.Unit)
				switch v.Unit {
				case "tasks/sec":
					throughput = append(throughput, v.Value)
				case "p99-workflow-latency-sec":
					// Convert seconds to nanoseconds for consistency with BENCHMARKING.md
					latency = append(latency, v.Value*1e9)
				}
			}

			// Don't double-count - rec.Values already contains what we need

			if len(throughput) == 0 || len(latency) == 0 {
				continue
			}

			// Store the data - accumulate raw values first
			configData := data[config]
			if configData == nil {
				configData = make(map[int]*BenchData)
				data[config] = configData
			}

			benchData := configData[limit]
			if benchData == nil {
				benchData = &BenchData{
					Config:        config,
					CombinerLimit: limit,
				}
				configData[limit] = benchData
			}

			// Accumulate raw values - we'll create samples later with all values
			benchData.ThroughputValues = append(benchData.ThroughputValues, throughput...)
			benchData.P99LatencyValues = append(benchData.P99LatencyValues, latency...)

		case *benchfmt.SyntaxError:
			log.Printf("Parse error: %v", rec)
		}
	}

	if err := benchFiles.Err(); err != nil {
		return nil, err
	}

	// Create samples with all accumulated values and compute summaries
	confidence := 0.95
	for _, configData := range data {
		for _, benchData := range configData {
			// Create samples with all values at once (this will sort them properly)
			benchData.ThroughputSample = benchmath.NewSample(benchData.ThroughputValues, &benchmath.DefaultThresholds)
			benchData.P99LatencySample = benchmath.NewSample(benchData.P99LatencyValues, &benchmath.DefaultThresholds)

			// Use AssumeNothing to get median-based summaries like benchstat
			benchData.ThroughputSummary = benchmath.AssumeNothing.Summary(benchData.ThroughputSample, confidence)
			benchData.P99LatencySummary = benchmath.AssumeNothing.Summary(benchData.P99LatencySample, confidence)
		}
	}

	return data, nil
}

func parseConfig(workload, duration, flushPeriod string) (Config, error) {
	var config Config

	config.Workload = workload

	// Parse duration
	dur, err := time.ParseDuration(duration)
	if err != nil {
		return config, fmt.Errorf("invalid duration: %s", duration)
	}
	config.Duration = dur

	// Parse flush period
	flush, err := time.ParseDuration(flushPeriod)
	if err != nil {
		return config, fmt.Errorf("invalid flushPeriod: %s", flushPeriod)
	}
	config.FlushPeriod = flush

	return config, nil
}

func parseCombinerLimit(combinerLimit string) (int, error) {
	return strconv.Atoi(combinerLimit)
}

func compareBenchmarks(baseline, current map[Config]map[int]*BenchData) []ComparisonResult {
	var results []ComparisonResult

	// Get all configs that exist in both datasets
	for config := range baseline {
		currentConfigData, exists := current[config]
		if !exists {
			continue
		}
		baselineConfigData := baseline[config]

		result := ComparisonResult{
			Config: config,
		}

		// Find best static limits (exclude unlimited -1)
		result.BaselineBest = findBestStaticLimit(baselineConfigData)
		result.CurrentBest = findBestStaticLimit(currentConfigData)

		// Get unlimited performance
		if unlimited := baselineConfigData[-1]; unlimited != nil {
			result.BaselineUnlimited = unlimited
		}
		if unlimited := currentConfigData[-1]; unlimited != nil {
			result.CurrentUnlimited = unlimited
		}

		// Calculate improvements
		baselineThroughput := result.BaselineBest.ThroughputSummary.Center
		currentThroughput := result.CurrentBest.ThroughputSummary.Center
		result.ThroughputImprovement = ((currentThroughput - baselineThroughput) / baselineThroughput) * 100

		baselineLatency := result.BaselineBest.P99LatencySummary.Center
		currentLatency := result.CurrentBest.P99LatencySummary.Center
		result.LatencyImprovement = ((baselineLatency - currentLatency) / baselineLatency) * 100

		// Test statistical significance using benchmath with AssumeNothing to match benchstat
		throughputComparison := benchmath.AssumeNothing.Compare(result.BaselineBest.ThroughputSample, result.CurrentBest.ThroughputSample)
		result.ThroughputSignificant = throughputComparison.P < 0.05

		latencyComparison := benchmath.AssumeNothing.Compare(result.BaselineBest.P99LatencySample, result.CurrentBest.P99LatencySample)
		result.LatencySignificant = latencyComparison.P < 0.05

		// Calculate latency threshold delta
		threshold := config.FlushPeriod + config.Duration
		thresholdNs := float64(threshold.Nanoseconds())
		result.LatencyThresholdDelta = ((currentLatency - thresholdNs) / thresholdNs) * 100

		// Calculate unlimited analysis with significance testing
		if result.BaselineUnlimited != nil && result.CurrentUnlimited != nil {
			// Throughput values
			baselineUnlimitedThroughput := result.BaselineUnlimited.ThroughputSummary.Center
			currentUnlimitedThroughput := result.CurrentUnlimited.ThroughputSummary.Center
			baselineBestThroughput := result.BaselineBest.ThroughputSummary.Center
			currentBestThroughput := result.CurrentBest.ThroughputSummary.Center

			// Latency values
			baselineUnlimitedLatency := result.BaselineUnlimited.P99LatencySummary.Center
			currentUnlimitedLatency := result.CurrentUnlimited.P99LatencySummary.Center
			baselineBestLatency := result.BaselineBest.P99LatencySummary.Center
			currentBestLatency := result.CurrentBest.P99LatencySummary.Center

			// ∞→∞ Throughput: unlimited-to-unlimited improvement
			result.UnlimitedThroughputImprovement = ((currentUnlimitedThroughput - baselineUnlimitedThroughput) / baselineUnlimitedThroughput) * 100
			unlimitedThroughputComparison := benchmath.AssumeNothing.Compare(result.BaselineUnlimited.ThroughputSample, result.CurrentUnlimited.ThroughputSample)
			result.UnlimitedThroughputSignificant = unlimitedThroughputComparison.P < 0.05

			// ∞→∞ Latency: unlimited-to-unlimited improvement (positive = lower latency = better)
			result.UnlimitedLatencyImprovement = ((baselineUnlimitedLatency - currentUnlimitedLatency) / baselineUnlimitedLatency) * 100
			unlimitedLatencyComparison := benchmath.AssumeNothing.Compare(result.BaselineUnlimited.P99LatencySample, result.CurrentUnlimited.P99LatencySample)
			result.UnlimitedLatencySignificant = unlimitedLatencyComparison.P < 0.05

			// B→∞ Throughput: baseline best to baseline unlimited percentage difference
			result.BaselineBestToUnlimitedThroughput = ((baselineBestThroughput - baselineUnlimitedThroughput) / baselineUnlimitedThroughput) * 100
			baselineBestToUnlimitedThroughputComparison := benchmath.AssumeNothing.Compare(result.BaselineBest.ThroughputSample, result.BaselineUnlimited.ThroughputSample)
			result.BaselineBestToUnlimitedThroughputSig = baselineBestToUnlimitedThroughputComparison.P < 0.05

			// B→∞ Latency: baseline best to baseline unlimited percentage difference (positive = higher latency = worse)
			result.BaselineBestToUnlimitedLatency = ((baselineBestLatency - baselineUnlimitedLatency) / baselineUnlimitedLatency) * 100
			baselineBestToUnlimitedLatencyComparison := benchmath.AssumeNothing.Compare(result.BaselineBest.P99LatencySample, result.BaselineUnlimited.P99LatencySample)
			result.BaselineBestToUnlimitedLatencySig = baselineBestToUnlimitedLatencyComparison.P < 0.05

			// C→∞ Throughput: current best to current unlimited percentage difference
			result.CurrentBestToUnlimitedThroughput = ((currentBestThroughput - currentUnlimitedThroughput) / currentUnlimitedThroughput) * 100
			currentBestToUnlimitedThroughputComparison := benchmath.AssumeNothing.Compare(result.CurrentBest.ThroughputSample, result.CurrentUnlimited.ThroughputSample)
			result.CurrentBestToUnlimitedThroughputSig = currentBestToUnlimitedThroughputComparison.P < 0.05

			// C→∞ Latency: current best to current unlimited percentage difference (positive = higher latency = worse)
			result.CurrentBestToUnlimitedLatency = ((currentBestLatency - currentUnlimitedLatency) / currentUnlimitedLatency) * 100
			currentBestToUnlimitedLatencyComparison := benchmath.AssumeNothing.Compare(result.CurrentBest.P99LatencySample, result.CurrentUnlimited.P99LatencySample)
			result.CurrentBestToUnlimitedLatencySig = currentBestToUnlimitedLatencyComparison.P < 0.05

			// Efficiency deltas: change in static vs unlimited efficiency
			result.ThroughputEfficiencyDelta = result.CurrentBestToUnlimitedThroughput - result.BaselineBestToUnlimitedThroughput
			result.LatencyEfficiencyDelta = result.CurrentBestToUnlimitedLatency - result.BaselineBestToUnlimitedLatency
		}

		results = append(results, result)
	}

	// Sort by ratio first, then workload, then duration
	slices.SortFunc(results, func(a, b ComparisonResult) int {
		// Primary: Sort by flush period / duration ratio
		ratioA := float64(a.Config.FlushPeriod) / float64(a.Config.Duration)
		ratioB := float64(b.Config.FlushPeriod) / float64(b.Config.Duration)
		if ratioA != ratioB {
			if ratioA < ratioB {
				return -1
			}
			return 1
		}
		// Secondary: Sort by workload
		if a.Config.Workload != b.Config.Workload {
			return strings.Compare(a.Config.Workload, b.Config.Workload)
		}
		// Tertiary: Sort by duration
		return int(a.Config.Duration - b.Config.Duration)
	})

	return results
}

func findBestStaticLimit(configData map[int]*BenchData) BenchData {
	var best *BenchData
	bestThroughput := 0.0

	// Only consider positive combiner limits (per BENCHMARKING.md)
	for limit, data := range configData {
		if limit <= 0 {
			continue
		}

		throughput := data.ThroughputSummary.Center
		if throughput > bestThroughput {
			bestThroughput = throughput
			best = data
		}
	}

	if best == nil {
		// Fallback - shouldn't happen with valid data
		for _, data := range configData {
			return *data
		}
	}

	return *best
}

// Create a compact custom style based on StyleLight
var tableStyle = table.StyleLight

func init() {
	tableStyle.Format.Header = text.FormatDefault
	tableStyle.Format.Footer = text.FormatDefault
	tableStyle.Format.Row = text.FormatDefault
	tableStyle.Format.HeaderVAlign = text.VAlignBottom
	tableStyle.Options.DrawBorder = false
	tableStyle.Options.SeparateHeader = true
	tableStyle.Options.SeparateColumns = true
	tableStyle.Options.SeparateRows = false
	tableStyle.Box.PaddingLeft = ""
	tableStyle.Box.PaddingRight = ""
	tableStyle.Box.MiddleVertical = " "
	tableStyle.Box.MiddleSeparator = tableStyle.Box.MiddleHorizontal
	tableStyle.Box.TopSeparator = tableStyle.Box.MiddleHorizontal
	tableStyle.Box.BottomSeparator = tableStyle.Box.MiddleHorizontal
	tableStyle.Box.TopLeft = ""
	tableStyle.Box.TopRight = ""
	tableStyle.Box.BottomLeft = ""
	tableStyle.Box.BottomRight = ""
	tableStyle.Box.Right = ""
	tableStyle.Box.Left = ""
	tableStyle.Box.RightSeparator = ""
	tableStyle.Box.LeftSeparator = ""
}

func printGatherOnlyResults(baselineData, currentData map[Config]map[int]*BenchData) {

	t := table.NewWriter()
	t.SetTitle("Gather-Only Benchmark Comparison")
	t.SetOutputMirror(os.Stdout)
	t.SetStyle(tableStyle)

	header := table.Row{"Configuration "}
	colConfigs := []table.ColumnConfig{
		{Number: 1, Align: text.AlignLeft},
	}

	headings := table.Row{"Throughput    ", "P99 Latency      ", "P99 Latency\n vs. Ideal"}
	for _, h := range headings {
		colConfigs = append(colConfigs,
			table.ColumnConfig{Number: len(colConfigs) + 1, Align: text.AlignRight},
			table.ColumnConfig{Number: len(colConfigs) + 2, Align: text.AlignLeft},
		)
		header = append(header, h, h)
	}

	t.SetColumnConfigs(colConfigs)
	t.AppendHeader(header, table.RowConfig{AutoMerge: true})

	// Collect configs that have gatherOnly data (combinerLimit=0) in both baseline and current
	var configs []Config
	for config := range baselineData {
		if baselineData[config][0] != nil && currentData[config] != nil && currentData[config][0] != nil {
			configs = append(configs, config)
		}
	}

	// Sort configs by workload and then duration
	slices.SortFunc(configs, func(a, b Config) int {
		if a.Workload != b.Workload {
			return strings.Compare(a.Workload, b.Workload)
		}
		return int(a.Duration - b.Duration)
	})

	var lastRatio float64 = -1
	for _, config := range configs {
		baseline := baselineData[config][0]
		current := currentData[config][0]

		// Add separator for ratio groups
		currentRatio := float64(config.FlushPeriod) / float64(config.Duration)
		if lastRatio != -1 && lastRatio != currentRatio {
			t.AppendSeparator()
		}
		lastRatio = currentRatio

		// Calculate percentage changes
		throughputChange := ((current.ThroughputSummary.Center - baseline.ThroughputSummary.Center) / baseline.ThroughputSummary.Center) * 100
		latencyChange := ((current.P99LatencySummary.Center - baseline.P99LatencySummary.Center) / baseline.P99LatencySummary.Center) * 100

		// Test significance
		throughputComparison := benchmath.AssumeNothing.Compare(baseline.ThroughputSample, current.ThroughputSample)
		latencyComparison := benchmath.AssumeNothing.Compare(baseline.P99LatencySample, current.P99LatencySample)

		throughputSig := throughputComparison.P < 0.05
		latencySig := latencyComparison.P < 0.05

		row := table.Row{config}
		row = append(row, formatChange(throughputChange, throughputSig)...)
		row = append(row, formatLatencyChange(latencyChange, latencySig)...)

		// Check threshold
		currentLatency := time.Duration(current.P99LatencySummary.Center)
		row = append(row, formatThreshold(config, currentLatency)...)

		t.AppendRow(row)
	}

	t.Render()
}

func printSummaryResults(results []ComparisonResult) {

	t := table.NewWriter()
	t.SetTitle("Static Combiner Concurrency Benchmark Comparison")
	t.SetOutputMirror(os.Stdout)
	t.SetStyle(tableStyle)

	bc := "   Best\nConcurrency  "
	header := table.Row{"Configuration", bc, bc, bc}
	colConfigs := []table.ColumnConfig{
		{Number: 1, Align: text.AlignLeft},
		{Number: 2, Align: text.AlignRight},
		{Number: 3, Align: text.AlignCenter},
		{Number: 4, Align: text.AlignLeft},
	}

	// Set headers with duplication for auto-merge
	headings := []string{"Throughput      ", "P99 Latency", "P99 Latency\n vs. Ideal"}
	for _, h := range headings {
		colConfigs = append(colConfigs,
			table.ColumnConfig{Number: len(colConfigs) + 1, Align: text.AlignRight},
			table.ColumnConfig{Number: len(colConfigs) + 2, Align: text.AlignLeft},
		)
		header = append(header, h, h) // Duplicate for value and significance columns
	}

	t.SetColumnConfigs(colConfigs)
	t.AppendHeader(header, table.RowConfig{AutoMerge: true})

	var lastRatio float64 = -1 // Track previous ratio for blank line insertion
	for _, result := range results {
		// Add separator before each new ratio group
		currentRatio := float64(result.Config.FlushPeriod) / float64(result.Config.Duration)
		if lastRatio != -1 && lastRatio != currentRatio {
			t.AppendSeparator()
		}
		lastRatio = currentRatio

		// Build row
		row := table.Row{
			result.Config,
			fmt.Sprintf("%5d", result.BaselineBest.CombinerLimit),
			"→",
			fmt.Sprintf("%-5d", result.CurrentBest.CombinerLimit),
		}

		// Add throughput value and significance
		row = append(row, formatChange(result.ThroughputImprovement, result.ThroughputSignificant)...)

		// Add latency value and significance
		row = append(row, formatLatencyChange(result.LatencyImprovement, result.LatencySignificant)...)

		// Add threshold value and significance
		currentLatency := time.Duration(result.CurrentBest.P99LatencySummary.Center)
		row = append(row, formatThreshold(result.Config, currentLatency)...)

		t.AppendRow(row)
	}

	t.Render()
}

func printUnlimitedAnalysis(results []ComparisonResult) {
	t := table.NewWriter()
	t.SetTitle("Dynamic Combiner Concurrency Benchmark Comparison")
	t.SetOutputMirror(os.Stdout)
	t.SetStyle(tableStyle)

	// Create header with merged cells using identical values and row config
	topheader := table.Row{""}
	midheader := table.Row{""}
	subheader := table.Row{"Configuration"}
	colConfigs := []table.ColumnConfig{
		{Number: 1, Align: text.AlignLeft},
	}

	addSubheaders := func() {
		subheadings := []string{"Throughput  ", "P99 Latency"}
		for _, sh := range subheadings {
			colConfigs = append(colConfigs,
				table.ColumnConfig{Number: len(colConfigs) + 1, Align: text.AlignRight},
				table.ColumnConfig{Number: len(colConfigs) + 2, Align: text.AlignLeft},
			)
			subheader = append(subheader, sh, sh)
		}
	}

	mh := "Dynamic vs Dynamic"
	topheader = append(topheader, "", "", "", "")
	midheader = append(midheader, mh, mh, mh, mh)
	addSubheaders()

	th := "Best Static vs Dynamic"
	midheadings := []string{"Baseline", "Current", "Change"}
	for _, mh := range midheadings {
		topheader = append(topheader, th, th, th, th)
		midheader = append(midheader, mh, mh, mh, mh)
		addSubheaders()
	}

	t.SetColumnConfigs(colConfigs)
	t.AppendHeader(topheader, table.RowConfig{AutoMerge: true})
	t.AppendHeader(midheader, table.RowConfig{AutoMerge: true})
	t.AppendHeader(subheader, table.RowConfig{AutoMerge: true})

	var lastRatio float64 = -1 // Track previous ratio for blank line insertion
	for _, result := range results {
		// Skip if no unlimited data
		if result.BaselineUnlimited == nil || result.CurrentUnlimited == nil {
			continue
		}

		// Add separator before each new ratio group
		currentRatio := float64(result.Config.FlushPeriod) / float64(result.Config.Duration)
		if lastRatio != -1 && lastRatio != currentRatio {
			t.AppendSeparator()
		}
		lastRatio = currentRatio

		// Build row
		row := table.Row{result.Config.String()}

		// Add unlimited throughput and latency values with significance
		row = append(row, formatChange(result.UnlimitedThroughputImprovement, result.UnlimitedThroughputSignificant)...)
		row = append(row, formatLatencyChange(result.UnlimitedLatencyImprovement, result.UnlimitedLatencySignificant)...)

		// Add baseline best-to-unlimited values with significance
		row = append(row, formatChange(result.BaselineBestToUnlimitedThroughput, result.BaselineBestToUnlimitedThroughputSig)...)
		row = append(row, formatChange(result.BaselineBestToUnlimitedLatency, result.BaselineBestToUnlimitedLatencySig)...)

		// Add current best-to-unlimited values with significance
		row = append(row, formatChange(result.CurrentBestToUnlimitedThroughput, result.CurrentBestToUnlimitedThroughputSig)...)
		row = append(row, formatChange(result.CurrentBestToUnlimitedLatency, result.CurrentBestToUnlimitedLatencySig)...)

		// Add efficiency delta values with significance
		row = append(row, formatEfficiencyDelta(result.ThroughputEfficiencyDelta)...)
		row = append(row, formatLatencyEfficiencyDelta(result.LatencyEfficiencyDelta)...)

		t.AppendRow(row)
	}

	t.Render()
}

func formatUnlimitedChange(improvement float64, significant bool) []any {
	if !significant {
		return []any{"~ ", ""}
	}

	value := fmt.Sprintf("%+.0f%%", improvement)
	if improvement == 0 {
		value = "0%"
	}

	sig := ""
	if improvement < 0 {
		sig = "(!)"
	}

	return []any{value, sig}
}

func formatUnlimitedRatio(percentDiff float64, significant bool) []any {
	if !significant {
		return []any{"~ ", ""}
	}

	value := fmt.Sprintf("%+.0f%%", percentDiff)

	sig := ""
	if percentDiff < 0 { // Flag any drops as concerning (static limit worse than unlimited)
		sig = "(!)"
	}

	return []any{value, sig}
}

func formatEfficiencyDelta(delta float64) []any {
	value := fmt.Sprintf("%+.0f%%", delta)

	sig := ""
	if delta < 0 {
		sig = "(!)"
	}

	return []any{value, sig}
}

func formatLatencyEfficiencyDelta(delta float64) []any {
	// For latency: negative delta = improvement (good), positive delta = degradation (bad)
	value := fmt.Sprintf("%+.0f%%", delta)

	sig := ""
	if delta > 0 { // Higher latency delta is bad
		sig = "(!)"
	}

	return []any{value, sig}
}

func formatChange(change float64, significant bool) []any {
	if !significant {
		return []any{"~ ", ""}
	}

	value := fmt.Sprintf("%+.1f%%", change)
	if change == 0 {
		value = "0%"
	}

	sig := ""
	if change < 0 {
		sig = "(!)"
	}

	return []any{value, sig}
}

func formatLatencyChange(latencyImprovement float64, significant bool) []any {
	if !significant {
		return []any{"~ ", ""}
	}

	// For latency: positive improvement = lower latency = good (show as negative)
	//              negative improvement = higher latency = bad (show as positive with !)
	actualChange := -latencyImprovement // Flip to show actual latency change

	value := fmt.Sprintf("%+.1f%%", actualChange)
	if actualChange == 0 {
		value = "0%"
	}

	sig := ""
	if actualChange > 0 { // Higher latency is bad
		sig = "(!)"
	}

	return []any{value, sig}
}

func formatThreshold(config Config, currentLatency time.Duration) []any {
	threshold := config.FlushPeriod + config.Duration
	thresholdNs := float64(threshold.Nanoseconds())
	currentLatencyNs := float64(currentLatency.Nanoseconds())

	if currentLatencyNs < thresholdNs {
		return []any{"~ ", ""}
	}

	delta := ((currentLatencyNs - thresholdNs) / thresholdNs) * 100
	return []any{fmt.Sprintf("+%.0f%%", delta), "(!)"}
}
