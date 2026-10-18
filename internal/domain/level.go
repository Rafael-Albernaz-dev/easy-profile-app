package domain

import (
	"fmt"
	"math"
)

// PercentageLevel is an immutable value object representing audio volume/gain
// in integer percentage points (0 to 100).
// Using integer basis points completely prevents IEEE-754 floating-point drift
// (e.g. 0.9400000000000001) during repeated ±2% adjustments.
type PercentageLevel struct {
	percent int
}

// NewPercentageLevel creates a clamped PercentageLevel in [0, 100].
func NewPercentageLevel(percent int) PercentageLevel {
	if percent < 0 {
		return PercentageLevel{percent: 0}
	}
	if percent > 100 {
		return PercentageLevel{percent: 100}
	}
	return PercentageLevel{percent: percent}
}

// NewPercentageFromRatio converts a PipeWire linear float ratio (0.00 to 1.00)
// to a cubic-root perceptual percentage point used by wpctl and GNOME.
func NewPercentageFromRatio(ratio float64) PercentageLevel {
	if ratio <= 0.0 {
		return PercentageLevel{percent: 0}
	}
	if ratio >= 1.0 {
		return PercentageLevel{percent: 100}
	}
	cbrt := math.Cbrt(ratio)
	rounded := int(math.Round(cbrt * 100.0))
	return NewPercentageLevel(rounded)
}

// Percent returns the integer percentage in [0, 100].
func (l PercentageLevel) Percent() int {
	return l.percent
}

// CubicRatio returns the cubic normalized ratio [0.00, 1.00] corresponding to wpctl scale.
func (l PercentageLevel) CubicRatio() float64 {
	return float64(l.percent) / 100.0
}

// LinearRatio returns the linear gain expected by PipeWire's internal channelVolumes (ratio^3).
func (l PercentageLevel) LinearRatio() float64 {
	r := l.CubicRatio()
	return r * r * r
}

// AddSteps adjusts the level by deltaPercent (e.g. +2 or -2), clamped strictly to [0, 100].
func (l PercentageLevel) AddSteps(deltaPercent int) PercentageLevel {
	return NewPercentageLevel(l.percent + deltaPercent)
}

// String formats the level as a percentage string (e.g. "95%").
func (l PercentageLevel) String() string {
	return fmt.Sprintf("%d%%", l.percent)
}

// FormatRatio formats the cubic ratio with 2 decimal places (e.g. "0.95").
func (l PercentageLevel) FormatRatio() string {
	return fmt.Sprintf("%.2f", l.CubicRatio())
}
