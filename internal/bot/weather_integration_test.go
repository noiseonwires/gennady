// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// weatherRouter dispatches by request path so a single test server can serve
// the weather, holidays and wikipedia endpoints.
func weatherRouter(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/forecast"):
			_, _ = w.Write([]byte(`{
				"current":{"time":"2026-06-09T12:00","temperature_2m":21.5,"wind_speed_10m":3.2,
					"rain":0,"precipitation":0,"showers":0,"cloud_cover":40,"weather_code":2,"snowfall":0},
				"hourly":{"time":["2026-06-09T12:00"],"temperature_2m":[21.5],"wind_speed_10m":[3.2],
					"rain":[0],"precipitation":[0],"showers":[0],"cloud_cover":[40],"weather_code":[2],"snowfall":[0]}
			}`))
		case strings.Contains(r.URL.Path, "/PublicHolidays"):
			_, _ = w.Write([]byte(`[{"id":"h1","startDate":"2026-06-09","endDate":"2026-06-09",
				"type":"Public","name":[{"language":"EN","text":"Test Holiday"}],"regionalScope":"National"}]`))
		case strings.Contains(r.URL.Path, "/onthisday/"):
			_, _ = w.Write([]byte(`{"selected":[{"text":"Something happened","year":1989}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func TestFetchCurrentWeather(t *testing.T) {
	b, _, rt := newIntegrationBot(t, weatherRouter(t))
	b.config.AI.ExternalData.WeatherLatitude = 50.0
	b.config.AI.ExternalData.WeatherLongitude = 14.0

	weather, raw, err := b.fetchCurrentWeather()
	require.NoError(t, err)
	require.NotNil(t, weather)
	assert.Equal(t, 21.5, weather.Current.Temperature2m)
	assert.NotEmpty(t, raw)
	assert.Contains(t, rt.last().Path, "/v1/forecast")
}

func TestFetchCurrentWeather_Error(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`boom`))
	})
	_, _, err := b.fetchCurrentWeather()
	require.Error(t, err)
}

func TestFetchHolidays(t *testing.T) {
	b, _, rt := newIntegrationBot(t, weatherRouter(t))

	holidays, err := b.fetchHolidays("CZ")
	require.NoError(t, err)
	require.Len(t, holidays, 1)
	require.Len(t, holidays[0].Name, 1)
	assert.Equal(t, "Test Holiday", holidays[0].Name[0].Text)
	assert.Contains(t, rt.last().Path, "/PublicHolidays")
}

func TestFetchHolidays_Error(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})
	_, err := b.fetchHolidays("CZ")
	require.Error(t, err)
}

func TestFetchWikipediaOnThisDay(t *testing.T) {
	b, _, rt := newIntegrationBot(t, weatherRouter(t))
	b.config.AI.ExternalData.WikipediaLanguage = "en"

	events, err := b.fetchWikipediaOnThisDay()
	require.NoError(t, err)
	require.NotEmpty(t, events)
	assert.Contains(t, events[0], "Something happened")
	assert.Contains(t, rt.last().Path, "/onthisday/")
}

func TestFetchWikipediaOnThisDay_Error(t *testing.T) {
	b, _, _ := newIntegrationBot(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	b.config.AI.ExternalData.WikipediaLanguage = "en"
	_, err := b.fetchWikipediaOnThisDay()
	require.Error(t, err)
}
