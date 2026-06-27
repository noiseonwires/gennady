// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWeatherCodeToDescription(t *testing.T) {
	assert.Equal(t, "clear sky", weatherCodeToDescription(0, "en"))
	assert.Equal(t, "ясно", weatherCodeToDescription(0, "ru"))
	// Unknown language -> empty.
	assert.Equal(t, "", weatherCodeToDescription(0, "fr"))
}

func TestFormatWeatherValues(t *testing.T) {
	// Clear, with wind only.
	out := formatWeatherValues(21.5, 3.2, 0, 0, 0, 0, 0, "en")
	assert.Contains(t, out, "21.5°C")
	assert.Contains(t, out, "clear sky")
	assert.Contains(t, out, "wind 3.2 km/h")
	assert.NotContains(t, out, "rain")

	// With rain.
	out = formatWeatherValues(10, 5, 2.5, 0, 0, 0, 61, "en")
	assert.Contains(t, out, "rain 2.5 mm")

	// With snow.
	out = formatWeatherValues(-2, 5, 0, 1.5, 0, 0, 71, "en")
	assert.Contains(t, out, "snow 1.5 cm")

	// Unknown language falls back to English labels.
	out = formatWeatherValues(15, 4, 0, 0, 0, 0, 0, "xx")
	assert.Contains(t, out, "wind 4.0 km/h")

	// Precipitation only shown when rain/snow/showers are all zero.
	out = formatWeatherValues(12, 3, 0, 0, 0, 1.2, 0, "en")
	assert.Contains(t, out, "precipitation 1.2 mm")
}

func TestFindHourIndex(t *testing.T) {
	times := []string{"2026-06-09T00:00", "2026-06-09T12:00", "2026-06-09T18:00"}
	assert.Equal(t, 1, findHourIndex(times, 12))
	assert.Equal(t, 2, findHourIndex(times, 18))
	assert.Equal(t, -1, findHourIndex(times, 23))
}

func TestFormatHourlyWeather(t *testing.T) {
	w := &openMeteoResponse{}
	w.Hourly.Time = []string{"2026-06-09T12:00"}
	w.Hourly.Temperature2m = []float64{20}
	w.Hourly.WindSpeed10m = []float64{5}
	w.Hourly.WeatherCode = []float64{0}

	out := formatHourlyWeather(w, 0, "en")
	assert.Contains(t, out, "20.0°C")

	// Out-of-range index -> empty.
	assert.Equal(t, "", formatHourlyWeather(w, 99, "en"))
}
