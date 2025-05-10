// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"fmt"
	"image/color"
	"log"
	"math"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/perf/benchfmt"
	"golang.org/x/perf/benchmath"
	"golang.org/x/perf/benchproc"
	"golang.org/x/perf/benchunit"
	"gonum.org/v1/plot"
	"gonum.org/v1/plot/palette/brewer"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/vg"
)

type seriesPoints struct {
	plotter.YErrorBars
	Labels []string
}

func (sp seriesPoints) Label(i int) string {
	return sp.Labels[i]
}

var _ plotter.Labeller = &seriesPoints{}

type chart struct {
	Title           string
	YAxisLabel      string
	XAxisLabel      string
	XTickLabels     []string
	XTickPositions  []float64
	SeriesLabels    []string
	SeriesPoints    []seriesPoints
	YAxisGrowFactor float64
	FileBasename    string
}

func setupPlot(c *chart) *plot.Plot {
	p := plot.New()

	p.Title.Text = c.Title
	p.X.Label.Text = c.XAxisLabel
	p.Y.Label.Text = c.YAxisLabel

	p.Title.TextStyle.Color = color.Gray{128}
	p.X.Color = color.Gray{128}
	p.Y.Color = color.Gray{128}
	p.X.Label.TextStyle.Color = color.Gray{128}
	p.Y.Label.TextStyle.Color = color.Gray{128}
	p.X.Tick.Color = color.Gray{128}
	p.Y.Tick.Color = color.Gray{128}
	p.X.Tick.Label.Color = color.Gray{128}
	p.Y.Tick.Label.Color = color.Gray{128}
	p.Legend.TextStyle.Color = color.Gray{128}

	p.X.Scale = plot.LogScale{}

	xTicks := make([]plot.Tick, len(c.XTickLabels))
	for i := range c.XTickLabels {
		t := &xTicks[i]
		t.Label = c.XTickLabels[i]
		t.Value = c.XTickPositions[i]
	}
	p.X.Tick.Marker = plot.ConstantTicks(xTicks)

	p.Legend.Top = true
	p.Legend.Left = true
	p.Legend.Padding = 1 * vg.Millimeter
	p.BackgroundColor = color.Transparent

	return p
}

func plotBars(c *chart) error {
	p := setupPlot(c)

	palette, err := brewer.GetPalette(brewer.TypeQualitative, "Paired", len(c.SeriesLabels))
	if err != nil {
		return err
	}
	colors := palette.Colors()

	barSpacing := vg.Points(3)
	barWidth := vg.Points(24)

	// Calculate the total width of the bar group, center to center.
	groupWidth := (barWidth + barSpacing) * vg.Length(len(c.SeriesPoints)-1)

	for i, label := range c.SeriesLabels {
		points := c.SeriesPoints[i]
		bc, err := newBarChart(points, barWidth)
		if err != nil {
			return err
		}
		bc.Offset = (barWidth+barSpacing)*vg.Length(i) - groupWidth/2
		bc.Color = colors[i]
		bc.LineStyle.Width = 0
		bc.ErrorStyle.Color = color.Gray{128}
		bc.ErrorStyle.Width = 0.2 * vg.Millimeter
		bc.LabelStyle = p.Y.Label.TextStyle
		bc.LabelStyle.Font.Size *= 0.7
		bc.LabelOffsets = make([]vg.Point, points.Len())
		for j := range bc.LabelOffsets {
			bc.LabelOffsets[j].Y = vg.Points(10)
		}

		p.Add(bc)
		p.Legend.Add(label, bc)
	}

	//p.Add(plotter.NewGlyphBoxes())

	return savePlot(c, p)
}

func savePlot(c *chart, p *plot.Plot) error {
	p.Y.Max *= c.YAxisGrowFactor

	// Create directory if it doesn't exist
	if err := os.MkdirAll("charts", 0755); err != nil {
		return err
	}

	// Save the plot
	if err := p.Save(9*vg.Inch, 6*vg.Inch, "charts/"+c.FileBasename+".svg"); err != nil {
		return err
	}

	return nil
}

type MethodKey struct{ benchproc.Key }
type WorkloadKey struct{ benchproc.Key }
type WorkloadDurationKey struct{ benchproc.Key }
type FlushPeriodKey struct{ benchproc.Key }

type Data struct {
	Sample     benchmath.Sample
	Summary    benchmath.Summary
	Reference  *Data
	Comparison benchmath.Comparison
}

func main() {
	var pp benchproc.ProjectionParser
	methodP, err := pp.Parse("/method,/combinerLimit", nil)
	if err != nil {
		log.Fatal(err)
	}
	workloadP, err := pp.Parse("/workload", nil)
	if err != nil {
		log.Fatal(err)
	}
	workloadDurationP, err := pp.Parse("/duration", nil)
	if err != nil {
		log.Fatal(err)
	}
	flushPeriodP, err := pp.Parse("/flushPeriod", nil)
	if err != nil {
		log.Fatal(err)
	}
	residueP := pp.Residue()

	dataByMethodWorkloadDurationFlushPeriodUnit := make(map[MethodKey]map[WorkloadKey]map[WorkloadDurationKey]map[FlushPeriodKey]map[string]*Data)
	methodKeySet := make(map[MethodKey]struct{})
	workloadKeySet := make(map[WorkloadKey]struct{})
	workloadDurationKeySet := make(map[WorkloadDurationKey]struct{})
	flushPeriodKeySet := make(map[FlushPeriodKey]struct{})
	var residues []benchproc.Key
	project := func(res *benchfmt.Result) {

		methodKey := MethodKey{methodP.Project(res)}
		dataByWorkloadDurationFlushPeriodUnit, ok := dataByMethodWorkloadDurationFlushPeriodUnit[methodKey]
		if !ok {
			dataByWorkloadDurationFlushPeriodUnit = make(map[WorkloadKey]map[WorkloadDurationKey]map[FlushPeriodKey]map[string]*Data)
			dataByMethodWorkloadDurationFlushPeriodUnit[methodKey] = dataByWorkloadDurationFlushPeriodUnit
			methodKeySet[methodKey] = struct{}{}
		}

		workloadKey := WorkloadKey{workloadP.Project(res)}
		dataByDurationFlushPeriodUnit, ok := dataByWorkloadDurationFlushPeriodUnit[workloadKey]
		if !ok {
			dataByDurationFlushPeriodUnit = make(map[WorkloadDurationKey]map[FlushPeriodKey]map[string]*Data)
			dataByWorkloadDurationFlushPeriodUnit[workloadKey] = dataByDurationFlushPeriodUnit
			workloadKeySet[workloadKey] = struct{}{}
		}

		workloadDurationKey := WorkloadDurationKey{workloadDurationP.Project(res)}
		dataByFlushPeriodUnit, ok := dataByDurationFlushPeriodUnit[workloadDurationKey]
		if !ok {
			dataByFlushPeriodUnit = make(map[FlushPeriodKey]map[string]*Data)
			dataByDurationFlushPeriodUnit[workloadDurationKey] = dataByFlushPeriodUnit
			workloadDurationKeySet[workloadDurationKey] = struct{}{}
		}

		flushPeriodKey := FlushPeriodKey{flushPeriodP.Project(res)}
		dataByUnit, ok := dataByFlushPeriodUnit[flushPeriodKey]
		if !ok {
			dataByUnit = make(map[string]*Data)
			dataByFlushPeriodUnit[flushPeriodKey] = dataByUnit
			flushPeriodKeySet[flushPeriodKey] = struct{}{}
		}

		for _, v := range res.Values {
			data := dataByUnit[v.Unit]
			if data == nil {
				data = &Data{}
				dataByUnit[v.Unit] = data
			}
			data.Sample.Values = append(data.Sample.Values, v.Value)
		}

		residue := residueP.Project(res)
		residues = append(residues, residue)
	}

	// Read the benchmark results.
	directAndGatherOnlyMatcher := regexp.MustCompile(`/method=(?:direct|gatherOnly)/`)
	var directAndGatherOnlyResults []*benchfmt.Result
	benchFiles := &benchfmt.Files{
		Paths:       os.Args[1:],
		AllowStdin:  true,
		AllowLabels: true,
	}
	for benchFiles.Scan() {
		var res *benchfmt.Result
		switch rec := benchFiles.Result(); rec := rec.(type) {
		case *benchfmt.Result:
			res = rec
		case *benchfmt.SyntaxError:
			// Report a non-fatal parse error.
			log.Print(err)
			continue
		default:
			// Unknown record type. Ignore.
			continue
		}

		// Scale values to match the number of tasks launched during each
		// iteration.
		if launchedPerOp, ok := res.Value("launched/op"); ok {
			res.Iters = int(math.Round(float64(res.Iters) * launchedPerOp))
			for i := range res.Values {
				v := &res.Values[i]
				if strings.HasSuffix(v.Unit, "/op") {
					v.Value /= launchedPerOp
				}
			}
		}

		// Need to save these to expand and match the appropriate sets of
		// flushPeriod values later.
		if directAndGatherOnlyMatcher.Match(res.Name) {
			directAndGatherOnlyResults = append(directAndGatherOnlyResults, res.Clone())
			continue
		}

		project(res)
	}
	if err := benchFiles.Err(); err != nil {
		log.Fatalf("Error reading benchmark files: %v", err)
	}

	flushPeriodKeySetByWorkloadDuration := make(map[WorkloadDurationKey]map[FlushPeriodKey]struct{})
	for _, dataByWorkloadDurationFlushPeriodUnit := range dataByMethodWorkloadDurationFlushPeriodUnit {
		for _, dataByDurationFlushPeriodUnit := range dataByWorkloadDurationFlushPeriodUnit {
			for workloadDurationKey, dataByFlushPeriodUnit := range dataByDurationFlushPeriodUnit {
				flushPeriodKeySet := flushPeriodKeySetByWorkloadDuration[workloadDurationKey]
				if flushPeriodKeySet == nil {
					flushPeriodKeySet = make(map[FlushPeriodKey]struct{})
					flushPeriodKeySetByWorkloadDuration[workloadDurationKey] = flushPeriodKeySet
				}
				for flushPeriodKey := range dataByFlushPeriodUnit {
					flushPeriodKeySet[flushPeriodKey] = struct{}{}
				}
			}
		}
	}

	flushPeriodKeys := make([]FlushPeriodKey, 0, len(flushPeriodKeySet))
	flushPeriods := make(map[FlushPeriodKey]time.Duration)
	for flushPeriodKey := range flushPeriodKeySet {
		flushPeriodKeys = append(flushPeriodKeys, flushPeriodKey)
		d, err := time.ParseDuration(flushPeriodKey.Get(flushPeriodP.Fields()[0]))
		if err != nil {
			log.Fatalf("Error parsing workload duration %q: %v\n", d, err)
		}
		flushPeriods[flushPeriodKey] = d
	}

	flushPeriodKeysByWorkloadDuration := make(map[WorkloadDurationKey][]FlushPeriodKey)
	flushPeriodKeysPerWorkloadDuration := -1
	for workloadDurationKey, flushPeriodKeyMap := range flushPeriodKeySetByWorkloadDuration {
		flushPeriodKeys := make([]FlushPeriodKey, 0, len(flushPeriodKeyMap))
		for flushPeriodKey := range flushPeriodKeyMap {
			flushPeriodKeys = append(flushPeriodKeys, flushPeriodKey)
		}
		slices.SortFunc(flushPeriodKeys, func(a, b FlushPeriodKey) int {
			dA, dB := flushPeriods[a], flushPeriods[b]
			switch {
			case dA < dB:
				return -1
			case dA == dB:
				return 0
			default:
				return 1
			}
		})
		flushPeriodKeysByWorkloadDuration[workloadDurationKey] = flushPeriodKeys
		if flushPeriodKeysPerWorkloadDuration == -1 {
			flushPeriodKeysPerWorkloadDuration = len(flushPeriodKeys)
		} else if len(flushPeriodKeys) != flushPeriodKeysPerWorkloadDuration {
			log.Fatalf("%v has %d flush period keys, expected %d", len(flushPeriodKeys), flushPeriodKeysPerWorkloadDuration)
		}
	}

	// Expand each direct and gatherOnly result to cover all relevant values for
	// flushPeriod
	flushPeriodReplacer := regexp.MustCompile(`(/flushPeriod=)[^/]*`)
	for _, res := range directAndGatherOnlyResults {
		workloadDurationKey := WorkloadDurationKey{workloadDurationP.Project(res)}
		for _, flushPeriodKey := range flushPeriodKeysByWorkloadDuration[workloadDurationKey] {
			res.Name = flushPeriodReplacer.ReplaceAll(res.Name, []byte(`${1}`+flushPeriodKey.Get(flushPeriodP.Fields()[0])))
			project(res)
		}
	}

	concurrencyLimits := make(map[MethodKey]int64)
	methodKeys := make([]MethodKey, 0, len(methodKeySet))
	for methodKey := range methodKeySet {
		methodKeys = append(methodKeys, methodKey)
		s := methodKey.Get(methodP.Fields()[1])
		x, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			log.Fatalf("Error parsing concurrency limit %q: %v\n", s, err)
		}
		concurrencyLimits[methodKey] = x
	}
	slices.SortFunc(methodKeys, func(a, b MethodKey) int {
		d := concurrencyLimits[a] - concurrencyLimits[b]
		switch {
		case d < 0:
			return -1
		case d == 0:
			return 0
		default:
			return 1
		}
	})
	methodKeyIndexes := make(map[MethodKey]int)
	for i, methodKey := range methodKeys {
		methodKeyIndexes[methodKey] = i
	}

	workloadKeys := make([]WorkloadKey, 0, len(workloadKeySet))
	for workloadKey := range workloadKeySet {
		workloadKeys = append(workloadKeys, workloadKey)
	}

	// Parse numeric values from keys
	workloadDurationKeys := make([]WorkloadDurationKey, 0, len(workloadDurationKeySet))
	workloadDurations := make(map[WorkloadDurationKey]time.Duration)
	for workloadDurationKey := range workloadDurationKeySet {
		workloadDurationKeys = append(workloadDurationKeys, workloadDurationKey)
		d, err := time.ParseDuration(workloadDurationKey.Get(workloadDurationP.Fields()[0]))
		if err != nil {
			log.Fatalf("Error parsing workload duration %q: %v\n", d, err)
		}
		workloadDurations[workloadDurationKey] = d
	}
	slices.SortFunc(workloadDurationKeys, func(a, b WorkloadDurationKey) int {
		d := workloadDurations[a] - workloadDurations[b]
		switch {
		case d < 0:
			return -1
		case d == 0:
			return 0
		default:
			return 1
		}
	})
	var minLogWorkloadDurationSpreadRatio float64
	for i := range workloadDurationKeys {
		if i == 0 {
			continue
		}
		logSpreadRatio := math.Log(float64(workloadDurations[workloadDurationKeys[i]]) / float64(workloadDurations[workloadDurationKeys[i-1]]))
		if i == 1 || logSpreadRatio < minLogWorkloadDurationSpreadRatio {
			minLogWorkloadDurationSpreadRatio = logSpreadRatio
		}
	}

	nonsingular := benchproc.NonSingularFields(residues)
	if len(nonsingular) > 0 {
		fmt.Printf("warning: results vary in %s\n", nonsingular)
	}

	var directMethodKey MethodKey
	for _, methodKey := range methodKeys {
		if methodKey.Get(methodP.Fields()[0]) == "direct" {
			directMethodKey = methodKey
			break
		}
	}

	// Connect reference values and do the math over the samples
	confidence := 0.95
	thresholds := benchmath.DefaultThresholds
	connectAndDoMath := func(
		dataByWorkloadDurationFlushPeriodUnit map[WorkloadKey]map[WorkloadDurationKey]map[FlushPeriodKey]map[string]*Data,
	) {
		for workloadKey, dataByDurationFlushPeriodUnit := range dataByWorkloadDurationFlushPeriodUnit {
			for workloadDurationKey, dataByFlushPeriodUnit := range dataByDurationFlushPeriodUnit {
				for flushPeriodKey, dataByUnit := range dataByFlushPeriodUnit {
					for unit, data := range dataByUnit {
						data.Sample = *benchmath.NewSample(data.Sample.Values, &thresholds)
						for _, w := range data.Sample.Warnings {
							log.Fatalf("sample warning: %v", w)
						}
						data.Summary = benchmath.AssumeNothing.Summary(&data.Sample, confidence)
						for _, w := range data.Summary.Warnings {
							if w.Error() != "all samples are equal" {
								log.Fatalf("summary warning: %v", w)
							}
						}
						data.Reference = dataByMethodWorkloadDurationFlushPeriodUnit[directMethodKey][workloadKey][workloadDurationKey][flushPeriodKey][unit]
						if data.Reference == nil {
							log.Fatalf("can't find reference for CombinerThroughput/workload=%v/duration=%v/flushPeriod=%v/method=%v/combinerLimit=%v-12 %v",
								workloadKey.Get(workloadP.Fields()[0]),
								workloadDurationKey.Get(workloadDurationP.Fields()[0]),
								flushPeriodKey.Get(flushPeriodP.Fields()[0]),
								directMethodKey.Get(methodP.Fields()[0]),
								directMethodKey.Get(methodP.Fields()[1]),
								unit,
							)
						}
						data.Comparison = benchmath.AssumeNothing.Compare(&data.Reference.Sample, &data.Sample)
						for _, w := range data.Comparison.Warnings {
							if w.Error() != "all samples are equal" {
								log.Fatalf("comparison: %v", w)
							}
						}
					}
				}
			}
		}
	}
	// Prepare the reference values first
	connectAndDoMath(dataByMethodWorkloadDurationFlushPeriodUnit[directMethodKey])
	for methodKey, dataByWorkloadDurationFlushPeriodUnit := range dataByMethodWorkloadDurationFlushPeriodUnit {
		if methodKey != directMethodKey {
			connectAndDoMath(dataByWorkloadDurationFlushPeriodUnit)
		}
	}

	// Create separate sets of charts for each workload type
	for _, workloadKey := range workloadKeys {

		workloadName := workloadKey.Get(workloadP.Fields()[0])
		workloadDisplayName := workloadName
		switch workloadName {
		case "processing":
			workloadDisplayName = "Processing"
		case "waiting":
			workloadDisplayName = "Waiting"
		}

		throughputChart := chart{
			Title:           fmt.Sprintf("Aggregation Workload Throughput (%s)", workloadDisplayName),
			XAxisLabel:      "Aggregation Workload Duration @ Flush Period",
			YAxisLabel:      "Tasks / Second",
			XTickLabels:     make([]string, flushPeriodKeysPerWorkloadDuration),
			XTickPositions:  make([]float64, flushPeriodKeysPerWorkloadDuration),
			SeriesLabels:    make([]string, len(methodKeys)),
			SeriesPoints:    make([]seriesPoints, len(methodKeys)),
			FileBasename:    workloadName + "_aggregation_throughput",
			YAxisGrowFactor: 1.2,
		}

		speedupChart := chart{
			Title:           fmt.Sprintf("Aggregation Workload Speedup (%s)", workloadDisplayName),
			XAxisLabel:      "Aggregation Workload Duration @ Flush Period",
			YAxisLabel:      "Throughput vs. Direct",
			XTickLabels:     make([]string, len(workloadDurationKeys)),
			XTickPositions:  make([]float64, len(workloadDurationKeys)),
			SeriesLabels:    make([]string, len(methodKeys)),
			SeriesPoints:    make([]seriesPoints, len(methodKeys)),
			FileBasename:    workloadName + "_aggregation_speedup",
			YAxisGrowFactor: 1.2,
		}

		allocationsChart := chart{
			Title:           fmt.Sprintf("Allocations Per Task Aggregated (%s)", workloadDisplayName),
			XAxisLabel:      "Aggregation Workload Duration @ Flush Period",
			YAxisLabel:      "Allocations / Task",
			XTickLabels:     make([]string, len(workloadDurationKeys)),
			XTickPositions:  make([]float64, len(workloadDurationKeys)),
			SeriesLabels:    make([]string, len(methodKeys)),
			SeriesPoints:    make([]seriesPoints, len(methodKeys)),
			FileBasename:    workloadName + "_aggregation_allocations",
			YAxisGrowFactor: 1.6,
		}

		allocBytesChart := chart{
			Title:           fmt.Sprintf("Bytes Allocated Per Task Aggregated (%s)", workloadDisplayName),
			XAxisLabel:      "Aggregation Workload Duration @ Flush Period",
			YAxisLabel:      "Allocated Bytes / Task",
			XTickLabels:     make([]string, len(workloadDurationKeys)),
			XTickPositions:  make([]float64, len(workloadDurationKeys)),
			SeriesLabels:    make([]string, len(methodKeys)),
			SeriesPoints:    make([]seriesPoints, len(methodKeys)),
			FileBasename:    workloadName + "_aggregation_bytes",
			YAxisGrowFactor: 1.6,
		}

		// Create lines for each workload type
		for lineIndex, methodKey := range methodKeys {

			methodName := methodKey.Get(methodP.Fields()[0])
			concurrencyLimit := concurrencyLimits[methodKey]

			var methodDisplayName string
			switch methodName {
			case "direct":
				methodDisplayName = "Direct Call"
			case "gatherOnly":
				methodDisplayName = "Gather Only"
			case "combine":
				methodDisplayName = fmt.Sprintf("Combine (limit %d)", concurrencyLimit)
			default:
				methodDisplayName = fmt.Sprintf("%s (limit %d)", methodName, concurrencyLimit)
			}

			throughputChart.SeriesLabels[lineIndex] = methodDisplayName
			throughputPoints := &throughputChart.SeriesPoints[lineIndex]
			throughputPoints.XYs = make(plotter.XYs, flushPeriodKeysPerWorkloadDuration)
			throughputPoints.YErrors = make(plotter.YErrors, flushPeriodKeysPerWorkloadDuration)
			throughputPoints.Labels = make([]string, flushPeriodKeysPerWorkloadDuration)

			speedupChart.SeriesLabels[lineIndex] = methodDisplayName
			speedupPoints := &speedupChart.SeriesPoints[lineIndex]
			speedupPoints.XYs = make(plotter.XYs, len(workloadDurationKeys))
			speedupPoints.YErrors = make(plotter.YErrors, len(workloadDurationKeys))
			speedupPoints.Labels = make([]string, len(workloadDurationKeys))

			allocationsChart.SeriesLabels[lineIndex] = methodDisplayName
			allocationsPoints := &allocationsChart.SeriesPoints[lineIndex]
			allocationsPoints.XYs = make(plotter.XYs, len(workloadDurationKeys))
			allocationsPoints.YErrors = make(plotter.YErrors, len(workloadDurationKeys))
			allocationsPoints.Labels = make([]string, len(workloadDurationKeys))

			allocBytesChart.SeriesLabels[lineIndex] = methodDisplayName
			allocBytesPoints := &allocBytesChart.SeriesPoints[lineIndex]
			allocBytesPoints.XYs = make(plotter.XYs, len(workloadDurationKeys))
			allocBytesPoints.YErrors = make(plotter.YErrors, len(workloadDurationKeys))
			allocBytesPoints.Labels = make([]string, len(workloadDurationKeys))

			workloadDurationKey := workloadDurationKeys[len(workloadDurationKeys)-1]
			for pointIndex, flushPeriodKey := range flushPeriodKeysByWorkloadDuration[workloadDurationKey] {

				flushPeriod := float64(flushPeriods[flushPeriodKey])

				xTickLabel := fmt.Sprintf("%s @ %s",
					workloadDurationKey.Get(workloadDurationP.Fields()[0]),
					flushPeriodKey.Get(flushPeriodP.Fields()[0]),
				)
				throughputChart.XTickPositions[pointIndex] = flushPeriod
				throughputChart.XTickLabels[pointIndex] = xTickLabel

				func() {
					unit := "completed/s"
					data := dataByMethodWorkloadDurationFlushPeriodUnit[methodKey][workloadKey][workloadDurationKey][flushPeriodKey][unit]

					throughputPoints.XYs[pointIndex].X = flushPeriod
					throughputPoints.XYs[pointIndex].Y = data.Summary.Center

					throughputPoints.YErrors[pointIndex].High = data.Summary.Hi - data.Summary.Center
					throughputPoints.YErrors[pointIndex].Low = data.Summary.Center - data.Summary.Lo

					throughputPoints.Labels[pointIndex] = formatSummary(&data.Summary, benchunit.Decimal)
				}()
			}

			for pointIndex, workloadDurationKey := range workloadDurationKeys {
				workloadDuration := float64(workloadDurations[workloadDurationKey])

				// Get the longest flush period for the workload duration
				workloadDurationFlushPeriodKeys := flushPeriodKeysByWorkloadDuration[workloadDurationKey]
				flushPeriodKey := workloadDurationFlushPeriodKeys[len(workloadDurationFlushPeriodKeys)-1]

				xTickLabel := fmt.Sprintf("%s @ %s",
					workloadDurationKey.Get(workloadDurationP.Fields()[0]),
					flushPeriodKey.Get(flushPeriodP.Fields()[0]),
				)
				speedupChart.XTickPositions[pointIndex] = workloadDuration
				speedupChart.XTickLabels[pointIndex] = xTickLabel
				allocationsChart.XTickPositions[pointIndex] = workloadDuration
				allocationsChart.XTickLabels[pointIndex] = xTickLabel
				allocBytesChart.XTickPositions[pointIndex] = workloadDuration
				allocBytesChart.XTickLabels[pointIndex] = xTickLabel

				func() {
					unit := "completed/s"
					data := dataByMethodWorkloadDurationFlushPeriodUnit[methodKey][workloadKey][workloadDurationKey][flushPeriodKey][unit]

					y := data.Summary.Center / data.Reference.Summary.Center
					speedupPoints.XYs[pointIndex].X = workloadDuration
					speedupPoints.XYs[pointIndex].Y = y

					plus := data.Summary.Hi - data.Summary.Center
					minus := data.Summary.Center - data.Summary.Lo
					refPlus := data.Reference.Summary.Hi - data.Reference.Summary.Center
					refMinus := data.Reference.Summary.Center - data.Reference.Summary.Lo
					variance := math.Sqrt((plus*minus)/(data.Summary.Center*data.Summary.Center) +
						(refPlus*refMinus)/(data.Reference.Summary.Center*data.Reference.Summary.Center))
					speedupPoints.YErrors[pointIndex].High = variance
					speedupPoints.YErrors[pointIndex].Low = variance

					speedupPoints.Labels[pointIndex] = formatSummary(&benchmath.Summary{
						Center: y,
						Hi:     y + variance,
						Lo:     y - variance,
					}, benchunit.Decimal)
				}()

				func() {
					unit := "allocs/op"
					data := dataByMethodWorkloadDurationFlushPeriodUnit[methodKey][workloadKey][workloadDurationKey][flushPeriodKey][unit]

					allocationsPoints.XYs[pointIndex].X = workloadDuration
					allocationsPoints.XYs[pointIndex].Y = data.Summary.Center

					allocationsPoints.YErrors[pointIndex].High = data.Summary.Hi - data.Summary.Center
					allocationsPoints.YErrors[pointIndex].Low = data.Summary.Center - data.Summary.Lo

					allocationsPoints.Labels[pointIndex] = formatSummary(&data.Summary, benchunit.Decimal)
				}()

				func() {
					unit := "B/op"
					data := dataByMethodWorkloadDurationFlushPeriodUnit[methodKey][workloadKey][workloadDurationKey][flushPeriodKey][unit]

					allocBytesPoints.XYs[pointIndex].X = workloadDuration
					allocBytesPoints.XYs[pointIndex].Y = data.Summary.Center

					allocBytesPoints.YErrors[pointIndex].High = data.Summary.Hi - data.Summary.Center
					allocBytesPoints.YErrors[pointIndex].Low = data.Summary.Center - data.Summary.Lo

					allocBytesPoints.Labels[pointIndex] = formatSummary(&data.Summary, benchunit.Decimal)
				}()
			}

			if err := plotBars(&throughputChart); err != nil {
				log.Fatalf("Error creating chart: %v", err)
			}

			if err := plotBars(&speedupChart); err != nil {
				log.Fatalf("Error creating chart: %v", err)
			}

			if err := plotBars(&allocationsChart); err != nil {
				log.Fatalf("Error creating chart: %v", err)
			}

			if err := plotBars(&allocBytesChart); err != nil {
				log.Fatalf("Error creating chart: %v", err)
			}
		}
	}

	fmt.Println("Charts generated successfully in the 'charts' directory.")
}

func formatRatio(n, d float64) string {
	switch {
	case d == 0:
		if n == 0 {
			return "0%"
		}
		return fmt.Sprintf("%.2g", n)
	case math.Abs(n/d) < 1:
		return fmt.Sprintf("%.2g%%", math.Round(100*n/d))
	default:
		return fmt.Sprintf("%.2gx", n/d)
	}
}

func formatSummary(s *benchmath.Summary, class benchunit.Class) string {
	var center string
	switch {
	case math.Abs(s.Center) > 0.0001 && math.Abs(s.Center) < 1:
		center = fmt.Sprintf("%.3f", s.Center)
	case math.Abs(s.Center) >= 1000 && math.Abs(s.Center) < 10000:
		center = fmt.Sprintf("%.0f", s.Center)
	default:
		center = benchunit.Scale(s.Center, class)
	}
	plus := formatRatio(s.Hi-s.Center, s.Center)
	minus := formatRatio(s.Center-s.Lo, s.Center)
	switch plus {
	case minus:
		return fmt.Sprintf("%s\n+/-\n%s", center, plus)
	default:
		return fmt.Sprintf("%s\n+%s\n-%s", center, plus, minus)
	}
}
