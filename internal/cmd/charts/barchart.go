// Derived from https://github.com/gonum/plot/blob/v0.16.0/plotter/barchart.go:
// Copyright ©2015 The Gonum Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"errors"
	"image/color"
	"math"

	"gonum.org/v1/plot"
	"gonum.org/v1/plot/font"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/text"
	"gonum.org/v1/plot/vg"
	"gonum.org/v1/plot/vg/draw"
)

// A BarChart presents grouped data with rectangular bars
// with lengths proportional to the data values.
type barChart struct {
	// The offset (X) and height (Y) of each bar.
	Bars plotter.XYs

	Errors plotter.YErrors

	Labels []string

	LabelOffsets []vg.Point

	// Width is the width of the bars.
	Width vg.Length

	// Color is the fill color of the bars.
	Color color.Color

	// LineStyle is the style of the outline of the bars.
	draw.LineStyle

	// ErrorStyle is the style of the error bars.
	ErrorStyle draw.LineStyle

	// LabelStyle is the style of the label text.
	LabelStyle text.Style

	// Offset is added to the X location of each bar.
	// When the Offset is zero, the bars are drawn
	// centered at their X location.
	Offset vg.Length

	// Horizontal dictates whether the bars should be in the vertical
	// (default) or horizontal direction. If Horizontal is true, all
	// X locations and distances referred to here will actually be Y
	// locations and distances.
	Horizontal bool

	// stackedOn is the bar chart upon which
	// this bar chart is stacked.
	stackedOn *barChart
}

// newBarChart returns a new bar chart with a single bar for each value. The
// bars' horizontal offsets are specified by their X values and heights by their
// Y values.
func newBarChart(bars plotter.XYer, width vg.Length) (*barChart, error) {
	if width <= 0 {
		return nil, errors.New("plotter: width parameter was not positive")
	}
	barsCopy, err := plotter.CopyXYs(bars)
	if err != nil {
		return nil, err
	}
	var yerrsCopy plotter.YErrors
	if yerrs, ok := bars.(plotter.YErrorer); ok {
		yerrsCopy = make(plotter.YErrors, bars.Len())
		for i := range yerrsCopy {
			yerrsCopy[i].Low, yerrsCopy[i].High = yerrs.YError(i)
		}
	}
	var labelsCopy []string
	if labels, ok := bars.(plotter.Labeller); ok {
		labelsCopy = make([]string, bars.Len())
		for i := range labelsCopy {
			labelsCopy[i] = labels.Label(i)
		}
	}
	return &barChart{
		Bars:       barsCopy,
		Errors:     yerrsCopy,
		Labels:     labelsCopy,
		Width:      width,
		Color:      color.Black,
		LineStyle:  plotter.DefaultLineStyle,
		ErrorStyle: plotter.DefaultLineStyle,
		LabelStyle: text.Style{
			Font:    font.From(plotter.DefaultFont, plotter.DefaultFontSize),
			Handler: plot.DefaultTextHandler,
		},
	}, nil
}

// BarHeight returns the maximum y value of the
// ith bar, taking into account any bars upon
// which it is stacked.
func (b *barChart) BarHeight(i int) float64 {
	ht := 0.0
	if b == nil {
		return 0
	}
	if i >= 0 && i < len(b.Bars) {
		ht += b.Bars[i].Y
	}
	if b.stackedOn != nil {
		ht += b.stackedOn.BarHeight(i)
	}
	return ht
}

// StackOn stacks a bar chart on top of another,
// and sets the Offset to that of the
// chart upon which it is being stacked.
func (b *barChart) StackOn(on *barChart) {
	b.Offset = on.Offset
	b.stackedOn = on
}

// Plot implements the plot.Plotter interface.
func (b *barChart) Plot(c draw.Canvas, plt *plot.Plot) {
	trCat, trVal := plt.Transforms(&c)
	if b.Horizontal {
		trCat, trVal = trVal, trCat
	}

	for i, bar := range b.Bars {
		cat := trCat(bar.X)
		if !b.Horizontal {
			if !c.ContainsX(cat) {
				continue
			}
		} else {
			if !c.ContainsY(cat) {
				continue
			}
		}
		cat += b.Offset
		catMin := cat - b.Width/2
		catMax := catMin + b.Width
		bottom := b.stackedOn.BarHeight(i)
		valMin := trVal(bottom)
		valMax := trVal(bottom + bar.Y)

		var pts []vg.Point
		var poly []vg.Point
		if !b.Horizontal {
			pts = []vg.Point{
				{X: catMin, Y: valMin},
				{X: catMin, Y: valMax},
				{X: catMax, Y: valMax},
				{X: catMax, Y: valMin},
			}
			poly = c.ClipPolygonY(pts)
		} else {
			pts = []vg.Point{
				{X: valMin, Y: catMin},
				{X: valMin, Y: catMax},
				{X: valMax, Y: catMax},
				{X: valMax, Y: catMin},
			}
			poly = c.ClipPolygonX(pts)
		}
		c.FillPolygon(b.Color, poly)

		var outline [][]vg.Point
		if !b.Horizontal {
			pts = append(pts, vg.Point{X: catMin, Y: valMin})
			outline = c.ClipLinesY(pts)
		} else {
			pts = append(pts, vg.Point{X: valMin, Y: catMin})
			outline = c.ClipLinesX(pts)
		}
		c.StrokeLines(b.LineStyle, outline...)

		// Error bars
		high := valMax
		if len(b.Errors) > 0 {
			valErr := b.Errors[i]
			low := trVal(bottom + bar.Y - math.Abs(valErr.Low))
			high = trVal(bottom + bar.Y + math.Abs(valErr.High))

			errBar := c.ClipLinesY([]vg.Point{{X: cat, Y: low}, {X: cat, Y: high}})
			c.StrokeLines(b.ErrorStyle, errBar...)
			drawCap := func(y vg.Length) {
				c.StrokeLine2(b.ErrorStyle, cat-b.Width/4, y, cat+b.Width/4, y)
			}
			drawCap(low)
			drawCap(high)
		}

		if len(b.Labels) > 0 {
			var offset vg.Point
			if len(b.LabelOffsets) > 0 {
				offset = b.LabelOffsets[i]
			}
			pt := vg.Point{X: cat + offset.X, Y: high + offset.Y}
			c.FillText(b.LabelStyle, pt, b.Labels[i])
		}
	}
}

// DataRange implements the plot.DataRanger interface.
func (b *barChart) DataRange() (xmin, xmax, ymin, ymax float64) {
	catMin := math.Inf(1)
	catMax := math.Inf(-1)
	valMin := math.Inf(1)
	valMax := math.Inf(-1)
	for i, bar := range b.Bars {
		catMin = math.Min(catMin, bar.X)
		catMax = math.Max(catMax, bar.X)

		valBot := b.stackedOn.BarHeight(i)
		valTop := valBot + bar.Y
		if len(b.Errors) > 0 {
			valBot = math.Min(valBot, valTop-math.Abs(b.Errors[i].Low))
			valTop += math.Abs(b.Errors[i].High)
		}
		valMin = math.Min(valMin, math.Min(valBot, valTop))
		valMax = math.Max(valMax, math.Max(valBot, valTop))
	}
	if !b.Horizontal {
		return catMin, catMax, valMin, valMax
	}
	return valMin, valMax, catMin, catMax
}

// GlyphBoxes implements the GlyphBoxer interface.
func (b *barChart) GlyphBoxes(plt *plot.Plot) []plot.GlyphBox {
	boxes := make([]plot.GlyphBox, len(b.Bars)+len(b.Labels))
	for i, bar := range b.Bars {
		if !b.Horizontal {
			boxes[i].X = plt.X.Norm(bar.X)
			boxes[i].Rectangle = vg.Rectangle{
				Min: vg.Point{X: b.Offset - b.Width/2},
				Max: vg.Point{X: b.Offset + b.Width/2},
			}
		} else {
			boxes[i].Y = plt.Y.Norm(bar.X)
			boxes[i].Rectangle = vg.Rectangle{
				Min: vg.Point{Y: b.Offset - b.Width/2},
				Max: vg.Point{Y: b.Offset + b.Width/2},
			}
		}
	}

	for i, label := range b.Labels {
		y := b.BarHeight(i)
		box := &boxes[len(b.Bars)+i]
		labelRect := b.LabelStyle.Rectangle(label)
		var offset vg.Point
		if len(b.LabelOffsets) > 0 {
			offset = b.LabelOffsets[i]
		}
		*box = boxes[i]
		box.Min.X += offset.X
		box.Max.X += offset.X
		box.Min.Y += offset.Y
		box.Max.Y += offset.Y
		if !b.Horizontal {
			box.Y = plt.Y.Norm(y)
			box.Max.Y += labelRect.Max.Y
		} else {
			box.X = plt.X.Norm(y)
			box.Max.X += labelRect.Max.X
		}
	}
	return boxes
}

// Thumbnail fulfills the plot.Thumbnailer interface.
func (b *barChart) Thumbnail(c *draw.Canvas) {
	pts := []vg.Point{
		{X: c.Min.X, Y: c.Min.Y},
		{X: c.Min.X, Y: c.Max.Y},
		{X: c.Max.X, Y: c.Max.Y},
		{X: c.Max.X, Y: c.Min.Y},
	}
	poly := c.ClipPolygonY(pts)
	c.FillPolygon(b.Color, poly)

	pts = append(pts, vg.Point{X: c.Min.X, Y: c.Min.Y})
	outline := c.ClipLinesY(pts)
	c.StrokeLines(b.LineStyle, outline...)
}
