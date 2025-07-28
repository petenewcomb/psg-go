// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"cmp"
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
)

func main() {
	flag.Parse()

	if len(flag.Args()) != 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <baseline_file> <current_file>\n", os.Args[0])
		os.Exit(1)
	}

	baseline, err := loadBenchmarkData(flag.Args()[0])
	if err != nil {
		log.Fatalf("Error loading baseline: %v", err)
	}

	current, err := loadBenchmarkData(flag.Args()[1])
	if err != nil {
		log.Fatalf("Error loading current: %v", err)
	}

	fmt.Println()
	printGatherOnlyComparison(baseline, current)
	fmt.Println()
	fmt.Println()
	printStaticCombinerConcurencyComparison(baseline, current)
	fmt.Println()
	fmt.Println()
	printDynamicCombinerConcurrencyComparison(baseline, current)
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

			limit, err := strconv.Atoi(combinerLimitKey.Get(combinerLimitField))
			if err != nil {
				log.Printf("Skipping benchmark with invalid combiner limit: %v", err)
				continue
			}

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

			for _, v := range rec.Values {
				switch v.Unit {
				case "tasks/sec":
					benchData.Throughput.Add(v)
				case "p50-combine-latency-sec":
					if limit != 0 {
						benchData.P50Latency.Add(v)
					}
				case "p99-combine-latency-sec":
					if limit != 0 {
						benchData.P99Latency.Add(v)
					}
				case "p50-gather-latency-sec":
					if limit == 0 {
						benchData.P50Latency.Add(v)
					}
				case "p99-gather-latency-sec":
					if limit == 0 {
						benchData.P99Latency.Add(v)
					}
				}
			}

		case *benchfmt.SyntaxError:
			log.Printf("Parse error: %v", rec)
		}
	}

	if err := benchFiles.Err(); err != nil {
		return nil, err
	}

	// Compute stats now that we've accumulated all the values
	for _, configData := range data {
		for _, benchData := range configData {
			benchData.ComputeStats()
		}
	}

	return data, nil
}

type Config struct {
	Workload    string
	Duration    time.Duration
	FlushPeriod time.Duration
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

func (c Config) String() string {
	ratio := float64(c.FlushPeriod) / float64(c.Duration)
	return fmt.Sprintf("%s %v (%.0fx)", c.Workload, c.Duration, ratio)
}

type Measurement struct {
	Values  []float64 // raw values collected before creating sample
	Sample  *benchmath.Sample
	Summary benchmath.Summary
}

func (m *Measurement) Add(v benchfmt.Value) {
	m.Values = append(m.Values, v.Value)
}

const confidence = 0.95

func (m *Measurement) ComputeStats() {
	m.Sample = benchmath.NewSample(m.Values, &benchmath.DefaultThresholds)
	m.Summary = benchmath.AssumeNothing.Summary(m.Sample, confidence)
}

type BenchData struct {
	Config        Config
	CombinerLimit int
	Throughput    Measurement
	P50Latency    Measurement
	P99Latency    Measurement
}

func (d *BenchData) ComputeStats() {
	d.Throughput.ComputeStats()
	d.P50Latency.ComputeStats()
	d.P99Latency.ComputeStats()
}

func findBestStaticLimit(configData map[int]*BenchData) *BenchData {
	var best *BenchData

	// Find lowest p99 latency static combiner limit
	for limit, data := range configData {
		if limit <= 0 {
			continue
		}

		latency := data.P99Latency.Summary.Center
		if best == nil || latency < best.P99Latency.Summary.Center {
			best = data
		}
	}

	// Find all candidates that have p99 latencies indistiguishable from the best
	candidates := []*BenchData{best}
	for limit, data := range configData {
		if limit <= 0 || data == best {
			continue
		}

		comparison := compareMeasurements(best.P99Latency, data.P99Latency)
		if comparison.P >= 0.05 {
			candidates = append(candidates, data)
		}
	}
	if len(candidates) == 1 {
		return candidates[0]
	}

	// Find candidates with best-in-class throughput
	slices.SortFunc(candidates, func(a, b *BenchData) int {
		return -cmp.Compare(a.Throughput.Summary.Center, b.Throughput.Summary.Center)
	})
	best = candidates[0]
	for i := len(candidates) - 1; i > 0; i-- {
		data := candidates[i]
		comparison := compareMeasurements(best.Throughput, data.Throughput)
		if comparison.P < 0.05 {
			candidates = slices.Delete(candidates, i, i+1)
		}
	}
	if len(candidates) == 1 {
		return candidates[0]
	}

	// Tiebreaker: find candidate with best p50 latency
	best = nil
	for _, data := range candidates {
		if best == nil || data.P50Latency.Summary.Center < best.P50Latency.Summary.Center {
			best = data
		}
	}
	return best
}

type ComparisonResult struct {
	Baseline   *BenchData
	Current    *BenchData
	Throughput benchmath.Comparison
	P50Latency benchmath.Comparison
	P99Latency benchmath.Comparison
}

func compareMeasurements(baseline, current Measurement) benchmath.Comparison {
	return benchmath.AssumeNothing.Compare(baseline.Sample, current.Sample)
}

func printGatherOnlyComparison(baseline, current map[Config]map[int]*BenchData) {

	t := table.NewWriter()
	t.SetTitle("Gather-Only Benchmark Comparison")
	t.SetOutputMirror(os.Stdout)
	t.SetStyle(tableStyle)

	tp1 := "Throughput"
	tp2 := "Change"
	lat := "Latency"
	topheader := table.Row{"", "", "", "", "", lat, lat, lat, lat, "", ""}
	midheader := table.Row{"", tp1, tp1}
	subheader := table.Row{"Configuration", tp2, tp2}
	colConfigs := []table.ColumnConfig{
		{Number: 1, Align: text.AlignLeft},
		{Number: 2, Align: text.AlignRight},
		{Number: 3, Align: text.AlignLeft},
	}

	// Set headers with duplication for auto-merge
	latcats := []string{"Change", "vs. Duration"}
	latps := []string{"P50", "P99"}
	for _, latcat := range latcats {
		for _, latp := range latps {
			colConfigs = append(colConfigs,
				table.ColumnConfig{Number: len(colConfigs) + 1, Align: text.AlignRight},
				table.ColumnConfig{Number: len(colConfigs) + 2, Align: text.AlignLeft},
			)
			midheader = append(midheader, latcat, latcat)
			subheader = append(subheader, latp, latp) // Duplicate for value and significance columns
		}
	}

	t.SetColumnConfigs(colConfigs)
	t.AppendHeader(topheader, table.RowConfig{AutoMerge: true})
	t.AppendHeader(midheader, table.RowConfig{AutoMerge: true})
	t.AppendHeader(subheader, table.RowConfig{AutoMerge: true})

	configs, pairs := collectPairs(baseline, current, func(byLimit map[int]*BenchData) *BenchData {
		return byLimit[0]
	})

	var lastRatio float64
	for i, config := range configs {
		// Add separator for ratio groups
		currentRatio := float64(config.FlushPeriod) / float64(config.Duration)
		if i != 0 && lastRatio != currentRatio {
			t.AppendSeparator()
		}
		lastRatio = currentRatio

		pair := pairs[config]
		baseline, current := pair[0], pair[1]

		row := table.Row{config}
		row = appendPerformanceChanges(row, config, baseline, current)
		row = appendLatenciesVsDuration(row, config, current)
		t.AppendRow(row)
	}

	t.Render()
}

func printStaticCombinerConcurencyComparison(baseline, current map[Config]map[int]*BenchData) {

	t := table.NewWriter()
	t.SetTitle("Static Combiner Concurrency Benchmark Comparison")
	t.SetOutputMirror(os.Stdout)
	t.SetStyle(tableStyle)

	bc1 := "Best"
	bc2 := "Concurrency"
	tp1 := "Throughput"
	tp2 := "Change"
	topheader := table.Row{"", "", "", "", "", ""}
	midheader := table.Row{"", bc1, bc1, bc1, tp1, tp1}
	subheader := table.Row{"Configuration", bc2, bc2, bc2, tp2, tp2}
	colConfigs := []table.ColumnConfig{
		{Number: 1, Align: text.AlignLeft},
		{Number: 2, Align: text.AlignRight},
		{Number: 3, Align: text.AlignCenter},
		{Number: 4, Align: text.AlignLeft},
		{Number: 5, Align: text.AlignRight},
		{Number: 6, Align: text.AlignLeft},
	}

	// Set headers with duplication for auto-merge
	lat := "Latency"
	latcats := []string{"Change", "vs. Duration"}
	latps := []string{"P50", "P99"}
	topheader = append(topheader, "", "", lat, lat, lat, lat, "", "")
	for _, latcat := range latcats {
		for _, latp := range latps {
			colConfigs = append(colConfigs,
				table.ColumnConfig{Number: len(colConfigs) + 1, Align: text.AlignRight},
				table.ColumnConfig{Number: len(colConfigs) + 2, Align: text.AlignLeft},
			)
			midheader = append(midheader, latcat, latcat)
			subheader = append(subheader, latp, latp) // Duplicate for value and significance columns
		}
	}

	t.SetColumnConfigs(colConfigs)
	t.AppendHeader(topheader, table.RowConfig{AutoMerge: true})
	t.AppendHeader(midheader, table.RowConfig{AutoMerge: true})
	t.AppendHeader(subheader, table.RowConfig{AutoMerge: true})

	configs, pairs := collectPairs(baseline, current, findBestStaticLimit)

	var lastRatio float64
	for i, config := range configs {
		// Add separator for ratio groups
		currentRatio := float64(config.FlushPeriod) / float64(config.Duration)
		if i != 0 && lastRatio != currentRatio {
			t.AppendSeparator()
		}
		lastRatio = currentRatio

		pair := pairs[config]
		baseline, current := pair[0], pair[1]

		// Build row
		row := table.Row{
			config,
			fmt.Sprintf("%5d", baseline.CombinerLimit),
			"→",
			fmt.Sprintf("%-5d", current.CombinerLimit),
		}
		row = appendPerformanceChanges(row, config, baseline, current)
		row = appendLatenciesVsDuration(row, config, current)
		t.AppendRow(row)
	}

	t.Render()
}

func printDynamicCombinerConcurrencyComparison(baseline, current map[Config]map[int]*BenchData) {
	t := table.NewWriter()
	t.SetTitle("Dynamic Combiner Concurrency Benchmark Comparison")
	t.SetOutputMirror(os.Stdout)
	t.SetStyle(tableStyle)

	// Create header with merged cells using identical values and row config
	topheader := table.Row{""}
	mid1header := table.Row{""}
	mid2header := table.Row{""}
	subheader := table.Row{"Configuration"}
	colConfigs := []table.ColumnConfig{
		{Number: 1, Align: text.AlignLeft},
	}

	addSubheaders := func() {
		tp := "Throughput"
		lat := "Latency"
		mid2header = append(mid2header, tp, tp, lat, lat, lat, lat)
		subheadings := []string{"Change", "P50", "P99"}
		for _, sh := range subheadings {
			colConfigs = append(colConfigs,
				table.ColumnConfig{Number: len(colConfigs) + 1, Align: text.AlignRight},
				table.ColumnConfig{Number: len(colConfigs) + 2, Align: text.AlignLeft},
			)
			subheader = append(subheader, sh, sh)
		}
	}

	mh1 := "Dynamic vs Dynamic"
	topheader = append(topheader, "", "", "", "", "", "")
	mid1header = append(mid1header, mh1, mh1, mh1, mh1, mh1, mh1)
	addSubheaders()

	th := "Best Static vs Dynamic"
	mid1headings := []string{"Baseline", "Current"}
	for _, mh1 := range mid1headings {
		topheader = append(topheader, th, th, th, th, th, th)
		mid1header = append(mid1header, mh1, mh1, mh1, mh1, mh1, mh1)
		addSubheaders()
	}

	t.SetColumnConfigs(colConfigs)
	t.AppendHeader(topheader, table.RowConfig{AutoMerge: true})
	t.AppendHeader(mid1header, table.RowConfig{AutoMerge: true})
	t.AppendHeader(mid2header, table.RowConfig{AutoMerge: true})
	t.AppendHeader(subheader, table.RowConfig{AutoMerge: true})

	bestStaticConfigs, bestStaticPairs := collectPairs(baseline, current, findBestStaticLimit)

	var lastRatio float64
	for i, config := range bestStaticConfigs {
		// Add separator for ratio groups
		currentRatio := float64(config.FlushPeriod) / float64(config.Duration)
		if i != 0 && lastRatio != currentRatio {
			t.AppendSeparator()
		}
		lastRatio = currentRatio

		bestStaticPair := bestStaticPairs[config]
		baselineBestStatic, currentBestStatic := bestStaticPair[0], bestStaticPair[1]

		baselineDynamic := baseline[config][-1]
		currentDynamic := current[config][-1]

		row := table.Row{config}
		row = appendPerformanceChanges(row, config, baselineDynamic, currentDynamic)
		row = appendPerformanceChanges(row, config, baselineBestStatic, baselineDynamic)
		row = appendPerformanceChanges(row, config, currentBestStatic, currentDynamic)
		t.AppendRow(row)
	}

	t.Render()
}

func collectPairs(
	baseline, current map[Config]map[int]*BenchData,
	chooseLimitFn func(byLimit map[int]*BenchData) *BenchData,
) ([]Config, map[Config][2]*BenchData) {

	var pairs map[Config][2]*BenchData
	for config, byLimit := range baseline {
		data := chooseLimitFn(byLimit)
		if data != nil {
			pair := pairs[config]
			pair[0] = data
			if pairs == nil {
				pairs = make(map[Config][2]*BenchData)
			}
			pairs[config] = pair
		}
	}
	for config, byLimit := range current {
		data := chooseLimitFn(byLimit)
		if data != nil {
			pair := pairs[config]
			pair[1] = data
			if pairs == nil {
				pairs = make(map[Config][2]*BenchData)
			}
			pairs[config] = pair
		}
	}

	// Sort configs by workload and then duration
	configs := make([]Config, 0, len(pairs))
	for config := range pairs {
		configs = append(configs, config)
	}
	slices.SortFunc(configs, func(a, b Config) int {
		// Primary: Sort by flush period / duration ratio
		result := cmp.Compare(
			float64(a.FlushPeriod)/float64(a.Duration),
			float64(b.FlushPeriod)/float64(b.Duration),
		)
		if result != 0 {
			return result
		}
		// Secondary: Sort by workload
		if a.Workload != b.Workload {
			return strings.Compare(a.Workload, b.Workload)
		}
		// Tertiary: Sort by duration
		return cmp.Compare(a.Duration, b.Duration)
	})

	return configs, pairs
}

func appendPerformanceChanges(row table.Row, config Config, baseline, current *BenchData) table.Row {
	row = append(row, formatChange(1, baseline.Throughput, current.Throughput)...)
	row = append(row, formatChange(-1, baseline.P50Latency, current.P50Latency)...)
	row = append(row, formatChange(-1, baseline.P99Latency, current.P99Latency)...)
	return row
}

func appendLatenciesVsDuration(row table.Row, config Config, data *BenchData) table.Row {
	row = append(row, formatLatencyVsDuration(config, data.P50Latency)...)
	row = append(row, formatLatencyVsDuration(config, data.P99Latency)...)
	return row
}

func formatChange(goodSign float64, baseline, current Measurement) []any {
	comparison := compareMeasurements(baseline, current)
	if comparison.P >= 0.01 {
		return insignificant
	}
	return []any{
		fmt.Sprintf("%+.1f%%",
			100*computeRelativeDifference(baseline.Summary.Center, current.Summary.Center)),
		formatIndicator(goodSign, baseline, current),
	}
}

func formatLatencyVsDuration(config Config, current Measurement) []any {
	duration := float64(config.Duration) / float64(time.Second)
	delta := current.Summary.Center / duration
	ind := neutralIndicator
	if computeRelativeDifference(current.Summary.Hi, duration) <= -0.05 {
		ind = badIndicator
	}
	return []any{fmt.Sprintf("%+.1f%%", 100*delta), ind}
}

func formatIndicator(goodSign float64, baseline, current Measurement) string {
	if goodSign < 0 {
		baseline, current = current, baseline
	}
	switch {
	case computeRelativeDifference(baseline.Summary.Hi, current.Summary.Lo) >= 0.05:
		return goodIndicator
	case computeRelativeDifference(baseline.Summary.Lo, current.Summary.Hi) <= -0.05:
		return badIndicator
	}
	return neutralIndicator
}

func computeRelativeDifference(baseline, current float64) float64 {
	return (current / baseline) - 1
}

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

const (
	neutralIndicator = " "
	goodIndicator    = "^"
	badIndicator     = "!"
)

const insignificantIndicator = " ~ "

var insignificant = []any{insignificantIndicator, neutralIndicator}
