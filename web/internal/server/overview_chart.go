package server

import (
	"fmt"
	"strings"

	"hookguard/web/internal/store"
)

// renderHourlyChart builds a server-rendered inline SVG bar chart of
// accepted (green, --ok) vs rejected (red, --reject) events per hour
// (DESIGN.md §6.2: "no charting library"). Bars are stacked: rejected on top
// of accepted, so a wholly-rejected hour is still visible even when small.
// A <title> plus an aria-label carry a plain-text summary so the chart is
// legible without color (DESIGN.md §2.2's colorblind rule applies here too).
func renderHourlyChart(buckets []store.HourlyCounts) string {
	const (
		width      = 720
		height     = 160
		barGap     = 2
		leftPad    = 0
		bottomPad  = 4
		plotHeight = height - bottomPad
	)

	if len(buckets) == 0 {
		return `<svg viewBox="0 0 720 160" xmlns="http://www.w3.org/2000/svg" role="img" aria-label="No event data for this window"></svg>`
	}

	maxTotal := 1 // avoid div-by-zero; a chart of all-zero buckets is still valid (flat baseline)
	totalAccepted, totalRejected := 0, 0
	for _, b := range buckets {
		if t := b.Accepted + b.Rejected; t > maxTotal {
			maxTotal = t
		}
		totalAccepted += b.Accepted
		totalRejected += b.Rejected
	}

	barWidth := (float64(width-leftPad) / float64(len(buckets))) - barGap
	if barWidth < 1 {
		barWidth = 1
	}

	var bars strings.Builder
	for i, b := range buckets {
		x := leftPad + float64(i)*(barWidth+barGap)
		acceptedH := float64(b.Accepted) / float64(maxTotal) * plotHeight
		rejectedH := float64(b.Rejected) / float64(maxTotal) * plotHeight

		acceptedY := plotHeight - acceptedH
		rejectedY := acceptedY - rejectedH

		if b.Accepted > 0 {
			fmt.Fprintf(&bars, `<rect x="%.2f" y="%.2f" width="%.2f" height="%.2f" fill="var(--ok)"><title>hour %d: %d accepted</title></rect>`,
				x, acceptedY, barWidth, acceptedH, b.Hour, b.Accepted)
		}
		if b.Rejected > 0 {
			fmt.Fprintf(&bars, `<rect x="%.2f" y="%.2f" width="%.2f" height="%.2f" fill="var(--reject)"><title>hour %d: %d rejected</title></rect>`,
				x, rejectedY, barWidth, rejectedH, b.Hour, b.Rejected)
		}
	}

	label := fmt.Sprintf("Events per hour over %d hours: %d accepted, %d rejected total", len(buckets), totalAccepted, totalRejected)
	return fmt.Sprintf(
		`<svg viewBox="0 0 %d %d" xmlns="http://www.w3.org/2000/svg" role="img" aria-label="%s"><title>%s</title>%s</svg>`,
		width, height, label, label, bars.String(),
	)
}
