// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package bot

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// openMeteoResponse is the response structure from Open-Meteo weather API.
type openMeteoResponse struct {
	Current struct {
		Time          string  `json:"time"`
		Temperature2m float64 `json:"temperature_2m"`
		WindSpeed10m  float64 `json:"wind_speed_10m"`
		Rain          float64 `json:"rain"`
		Precipitation float64 `json:"precipitation"`
		Showers       float64 `json:"showers"`
		CloudCover    int     `json:"cloud_cover"`
		WeatherCode   int     `json:"weather_code"`
		Snowfall      float64 `json:"snowfall"`
	} `json:"current"`
	Hourly struct {
		Time          []string  `json:"time"`
		Temperature2m []float64 `json:"temperature_2m"`
		WindSpeed10m  []float64 `json:"wind_speed_10m"`
		Rain          []float64 `json:"rain"`
		Precipitation []float64 `json:"precipitation"`
		Showers       []float64 `json:"showers"`
		CloudCover    []float64 `json:"cloud_cover"`
		WeatherCode   []float64 `json:"weather_code"`
		Snowfall      []float64 `json:"snowfall"`
	} `json:"hourly"`
}

// Holiday represents a public holiday from the Open Holidays API.
type Holiday struct {
	ID        string `json:"id"`
	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate"`
	Type      string `json:"type"`
	Name      []struct {
		Language string `json:"language"`
		Text     string `json:"text"`
	} `json:"name"`
	RegionalScope string `json:"regionalScope"`
}

// WikipediaOnThisDayResponse is the response from the Wikimedia "on this day" API.
type WikipediaOnThisDayResponse struct {
	Selected []struct {
		Text string `json:"text"`
		Year int    `json:"year"`
	} `json:"selected"`
}

// weatherDescriptions maps WMO 4677 weather codes to human-readable strings keyed by language.
var weatherDescriptions = map[string]map[int]string{
	"ru": {
		0: "ясно", 1: "преимущественно ясно", 2: "переменная облачность", 3: "пасмурно",
		4: "дымка от пожаров или промышленного дыма", 5: "мгла", 6: "пыль в воздухе",
		7: "пыль или песок подняты ветром", 8: "пыльные или песчаные вихри", 9: "пыльная или песчаная буря",
		10: "дымка", 11: "клочья тумана", 12: "поземный туман", 13: "молнии без грома",
		14: "осадки, не достигающие земли", 15: "осадки вдали", 16: "осадки вблизи",
		17: "гроза без осадков", 18: "шквалы", 19: "смерч",
		20: "морось или снежные зёрна (недавно)", 21: "дождь (недавно)", 22: "снег (недавно)",
		23: "дождь со снегом (недавно)", 24: "ледяная морось или ледяной дождь (недавно)",
		25: "ливневый дождь (недавно)", 26: "ливневый снег (недавно)", 27: "ливень с градом (недавно)",
		28: "туман (недавно)", 29: "гроза (недавно)",
		30: "слабая пыльная буря, ослабевает", 31: "слабая пыльная буря", 32: "слабая пыльная буря, усиливается",
		33: "сильная пыльная буря, ослабевает", 34: "сильная пыльная буря", 35: "сильная пыльная буря, усиливается",
		36: "слабая низовая метель", 37: "сильная низовая метель", 38: "слабая верховая метель", 39: "сильная верховая метель",
		40: "туман на расстоянии", 41: "туман клочьями", 42: "туман, небо видно, ослабевает",
		48: "туман с изморозью, небо видно", 49: "туман с изморозью, небо не видно",
		50: "слабая прерывистая морось", 51: "слабая непрерывная морось",
		52: "умеренная прерывистая морось", 53: "умеренная непрерывная морось",
		54: "сильная прерывистая морось", 55: "сильная непрерывная морось",
		56: "слабая ледяная морось", 57: "умеренная или сильная ледяная морось",
		58: "слабая морось с дождём", 59: "умеренная или сильная морось с дождём",
		60: "слабый прерывистый дождь", 61: "слабый непрерывный дождь",
		62: "умеренный прерывистый дождь", 63: "умеренный непрерывный дождь",
		64: "сильный прерывистый дождь", 65: "сильный непрерывный дождь",
		66: "слабый ледяной дождь", 67: "умеренный или сильный ледяной дождь",
		68: "слабый дождь со снегом", 69: "умеренный или сильный дождь со снегом",
		70: "слабый прерывистый снег", 71: "слабый непрерывный снег",
		72: "умеренный прерывистый снег", 73: "умеренный непрерывный снег",
		74: "сильный прерывистый снег", 75: "сильный непрерывный снег",
		76: "алмазная пыль", 77: "снежные зёрна", 78: "отдельные снежные кристаллы", 79: "ледяная крупа",
		80: "слабый ливень", 81: "умеренный или сильный ливень", 82: "очень сильный ливень",
		83: "слабый ливень дождя со снегом", 84: "умеренный или сильный ливень дождя со снегом",
		85: "слабый снегопад", 86: "умеренный или сильный снегопад",
		87: "слабый ливень с крупой или мелким градом", 88: "умеренный или сильный ливень с крупой",
		89: "слабый ливень с градом", 90: "умеренный или сильный ливень с градом",
		91: "слабый дождь, недавняя гроза", 92: "умеренный или сильный дождь, недавняя гроза",
		93: "слабый снег или град, недавняя гроза", 94: "умеренный или сильный снег или град, недавняя гроза",
		95: "слабая или умеренная гроза с осадками", 96: "слабая или умеренная гроза с градом",
		97: "сильная гроза с осадками", 98: "гроза с пыльной бурей", 99: "сильная гроза с градом",
	},
	"en": {
		0: "clear sky", 1: "mainly clear", 2: "partly cloudy", 3: "overcast",
		4: "haze from fires or industrial smoke", 5: "haze", 6: "dust in the air",
		7: "dust or sand raised by wind", 8: "dust or sand whirls", 9: "dust or sandstorm",
		10: "mist", 11: "patches of fog", 12: "ground fog", 13: "lightning without thunder",
		14: "precipitation not reaching ground", 15: "distant precipitation", 16: "nearby precipitation",
		17: "thunderstorm without precipitation", 18: "squalls", 19: "tornado",
		20: "drizzle or snow grains (recent)", 21: "rain (recent)", 22: "snow (recent)",
		23: "rain and snow (recent)", 24: "freezing drizzle or freezing rain (recent)",
		25: "rain shower (recent)", 26: "snow shower (recent)", 27: "hail shower (recent)",
		28: "fog (recent)", 29: "thunderstorm (recent)",
		30: "slight dust storm, decreasing", 31: "slight dust storm", 32: "slight dust storm, increasing",
		33: "heavy dust storm, decreasing", 34: "heavy dust storm", 35: "heavy dust storm, increasing",
		36: "slight drifting snow", 37: "heavy drifting snow", 38: "slight blowing snow", 39: "heavy blowing snow",
		40: "fog at a distance", 41: "patchy fog", 42: "fog, sky visible, thinning",
		48: "rime fog, sky visible", 49: "rime fog, sky not visible",
		50: "slight intermittent drizzle", 51: "slight continuous drizzle",
		52: "moderate intermittent drizzle", 53: "moderate continuous drizzle",
		54: "heavy intermittent drizzle", 55: "heavy continuous drizzle",
		56: "slight freezing drizzle", 57: "moderate or heavy freezing drizzle",
		58: "slight drizzle and rain", 59: "moderate or heavy drizzle and rain",
		60: "slight intermittent rain", 61: "slight continuous rain",
		62: "moderate intermittent rain", 63: "moderate continuous rain",
		64: "heavy intermittent rain", 65: "heavy continuous rain",
		66: "slight freezing rain", 67: "moderate or heavy freezing rain",
		68: "slight rain and snow", 69: "moderate or heavy rain and snow",
		70: "slight intermittent snow", 71: "slight continuous snow",
		72: "moderate intermittent snow", 73: "moderate continuous snow",
		74: "heavy intermittent snow", 75: "heavy continuous snow",
		76: "diamond dust", 77: "snow grains", 78: "isolated snow crystals", 79: "ice pellets",
		80: "slight rain shower", 81: "moderate or heavy rain shower", 82: "very heavy rain shower",
		83: "slight rain and snow shower", 84: "moderate or heavy rain and snow shower",
		85: "slight snow shower", 86: "moderate or heavy snow shower",
		87: "slight shower with hail", 88: "moderate or heavy shower with hail",
		89: "slight hailstorm", 90: "moderate or heavy hailstorm",
		91: "slight rain, recent thunderstorm", 92: "moderate or heavy rain, recent thunderstorm",
		93: "slight snow or hail, recent thunderstorm", 94: "moderate or heavy snow or hail, recent thunderstorm",
		95: "slight or moderate thunderstorm with precipitation", 96: "slight or moderate thunderstorm with hail",
		97: "heavy thunderstorm with precipitation", 98: "thunderstorm with dust storm", 99: "heavy thunderstorm with hail",
	},
}

// weatherCodeToDescription converts a WMO 4677 weather code to a description in the given language ("ru" or "en").
func weatherCodeToDescription(code int, lang string) string {
	if descs, ok := weatherDescriptions[lang]; ok {
		return descs[code]
	}
	return ""
}

// weatherLabels holds localised unit labels for weather formatting.
var weatherLabels = map[string]struct {
	Wind, Rain, Snow, Showers, Precipitation string
}{
	"ru": {"ветер %.1f км/ч", "дождь %.1f мм", "снег %.1f см", "ливни %.1f мм", "осадки %.1f мм"},
	"en": {"wind %.1f km/h", "rain %.1f mm", "snow %.1f cm", "showers %.1f mm", "precipitation %.1f mm"},
}

// formatWeatherValues formats weather parameters into a human-readable string in the given language.
func formatWeatherValues(temp, wind, rain, snowfall, showers, precipitation float64, weatherCode int, lang string) string {
	weatherDesc := weatherCodeToDescription(weatherCode, lang)
	labels := weatherLabels[lang]
	if labels.Wind == "" {
		labels = weatherLabels["en"]
	}

	var parts []string
	parts = append(parts, fmt.Sprintf("%.1f°C", temp))
	if weatherDesc != "" {
		parts = append(parts, weatherDesc)
	}
	parts = append(parts, fmt.Sprintf(labels.Wind, wind))
	if rain > 0 {
		parts = append(parts, fmt.Sprintf(labels.Rain, rain))
	}
	if snowfall > 0 {
		parts = append(parts, fmt.Sprintf(labels.Snow, snowfall))
	}
	if showers > 0 {
		parts = append(parts, fmt.Sprintf(labels.Showers, showers))
	}
	if precipitation > 0 && rain == 0 && snowfall == 0 && showers == 0 {
		parts = append(parts, fmt.Sprintf(labels.Precipitation, precipitation))
	}
	return strings.Join(parts, ", ")
}

// formatHourlyWeather formats weather data for a specific hour index from the hourly forecast.
func formatHourlyWeather(w *openMeteoResponse, hourIndex int, lang string) string {
	h := w.Hourly
	if hourIndex < 0 || hourIndex >= len(h.Time) {
		return ""
	}

	safeFloat := func(arr []float64, idx int) float64 {
		if idx < len(arr) {
			return arr[idx]
		}
		return 0
	}

	return formatWeatherValues(
		safeFloat(h.Temperature2m, hourIndex),
		safeFloat(h.WindSpeed10m, hourIndex),
		safeFloat(h.Rain, hourIndex),
		safeFloat(h.Snowfall, hourIndex),
		safeFloat(h.Showers, hourIndex),
		safeFloat(h.Precipitation, hourIndex),
		int(safeFloat(h.WeatherCode, hourIndex)),
		lang,
	)
}

// findHourIndex returns the index in the hourly time array for the given target hour (0-23).
// Returns -1 if not found.
func findHourIndex(times []string, targetHour int) int {
	targetSuffix := fmt.Sprintf("T%02d:00", targetHour)
	for i, t := range times {
		if strings.HasSuffix(t, targetSuffix) {
			return i
		}
	}
	return -1
}

// buildUsedWeatherJSON builds an indented JSON document containing only the
// weather data actually referenced by the morning greeting: the current
// conditions and the hourly forecast entries at the given indices. This avoids
// storing the full-day raw API response when only a few time points are used.
func buildUsedWeatherJSON(w *openMeteoResponse, hourIndices []int) (string, error) {
	if w == nil {
		return "", fmt.Errorf("nil weather response")
	}

	used := struct {
		Current interface{} `json:"current"`
		Hourly  struct {
			Time          []string  `json:"time"`
			Temperature2m []float64 `json:"temperature_2m"`
			WindSpeed10m  []float64 `json:"wind_speed_10m"`
			Rain          []float64 `json:"rain"`
			Precipitation []float64 `json:"precipitation"`
			Showers       []float64 `json:"showers"`
			CloudCover    []float64 `json:"cloud_cover"`
			WeatherCode   []float64 `json:"weather_code"`
			Snowfall      []float64 `json:"snowfall"`
		} `json:"hourly"`
	}{Current: w.Current}

	h := w.Hourly
	at := func(arr []float64, idx int) float64 {
		if idx >= 0 && idx < len(arr) {
			return arr[idx]
		}
		return 0
	}
	for _, idx := range hourIndices {
		if idx < 0 || idx >= len(h.Time) {
			continue
		}
		used.Hourly.Time = append(used.Hourly.Time, h.Time[idx])
		used.Hourly.Temperature2m = append(used.Hourly.Temperature2m, at(h.Temperature2m, idx))
		used.Hourly.WindSpeed10m = append(used.Hourly.WindSpeed10m, at(h.WindSpeed10m, idx))
		used.Hourly.Rain = append(used.Hourly.Rain, at(h.Rain, idx))
		used.Hourly.Precipitation = append(used.Hourly.Precipitation, at(h.Precipitation, idx))
		used.Hourly.Showers = append(used.Hourly.Showers, at(h.Showers, idx))
		used.Hourly.CloudCover = append(used.Hourly.CloudCover, at(h.CloudCover, idx))
		used.Hourly.WeatherCode = append(used.Hourly.WeatherCode, at(h.WeatherCode, idx))
		used.Hourly.Snowfall = append(used.Hourly.Snowfall, at(h.Snowfall, idx))
	}

	data, err := json.MarshalIndent(used, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// fetchCurrentWeather fetches current weather and today's hourly forecast from Open-Meteo.
// It returns the parsed response, the raw response body (for diagnostics or extra_info),
// and any error encountered.
func (b *Bot) fetchCurrentWeather() (*openMeteoResponse, []byte, error) {
	url := fmt.Sprintf("https://api.open-meteo.com/v1/forecast?latitude=%f&longitude=%f&current=temperature_2m,wind_speed_10m,rain,precipitation,showers,cloud_cover,weather_code,snowfall&hourly=temperature_2m,wind_speed_10m,rain,precipitation,showers,cloud_cover,weather_code,snowfall&timezone=auto&forecast_days=1",
		b.config.AI.ExternalData.WeatherLatitude, b.config.AI.ExternalData.WeatherLongitude)

	res, err := b.doAPIWithRetries("weather", &http.Client{Timeout: 30 * time.Second}, 2, func() (*http.Request, []byte, error) {
		req, rerr := http.NewRequest("GET", url, nil)
		if rerr != nil {
			return nil, nil, rerr
		}
		return req, nil, nil
	})
	if err != nil {
		return nil, nil, err
	}
	body := res.Body
	if !res.IsOK() {
		b.logAPIError("weather", res.StatusCode, body, nil)
		return nil, body, fmt.Errorf("weather API error %d: %s", res.StatusCode, string(body))
	}

	var w openMeteoResponse
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, body, err
	}

	return &w, body, nil
}

// fetchHolidays fetches public holidays for the given country for today.
func (b *Bot) fetchHolidays(countryIso string) ([]Holiday, error) {
	today := time.Now().Format("2006-01-02")
	url := fmt.Sprintf("https://openholidaysapi.org/PublicHolidays?countryIsoCode=%s&languageIsoCode=EN&validFrom=%s&validTo=%s", countryIso, today, today)

	res, err := b.doAPIWithRetries("holidays", &http.Client{Timeout: 30 * time.Second}, 2, func() (*http.Request, []byte, error) {
		req, rerr := http.NewRequest("GET", url, nil)
		if rerr != nil {
			return nil, nil, rerr
		}
		req.Header.Set("Accept", "text/json")
		return req, nil, nil
	})
	if err != nil {
		return nil, err
	}
	body := res.Body
	if !res.IsOK() {
		b.logAPIError("holidays", res.StatusCode, body, nil)
		return nil, fmt.Errorf("holidays API error %d: %s", res.StatusCode, string(body))
	}

	var hs []Holiday
	if err := json.Unmarshal(body, &hs); err != nil {
		return nil, err
	}

	return hs, nil
}

// fetchWikipediaOnThisDay fetches historical events that happened on this day from Wikipedia.
func (b *Bot) fetchWikipediaOnThisDay() ([]string, error) {
	now := time.Now()
	month := int(now.Month())
	day := now.Day()

	url := fmt.Sprintf("https://api.wikimedia.org/feed/v1/wikipedia/%s/onthisday/selected/%d/%d",
		b.config.AI.ExternalData.WikipediaLanguage, month, day)

	res, err := b.doAPIWithRetries("wikipedia", &http.Client{Timeout: 30 * time.Second}, 2, func() (*http.Request, []byte, error) {
		req, rerr := http.NewRequest("GET", url, nil)
		if rerr != nil {
			return nil, nil, rerr
		}
		req.Header.Set("User-Agent", BotName+"Bot/1.0 (https://github.com/noiseonwires/gennady)")
		return req, nil, nil
	})
	if err != nil {
		return nil, err
	}
	body := res.Body
	if !res.IsOK() {
		b.logAPIError("wikipedia", res.StatusCode, body, nil)
		return nil, fmt.Errorf("wikipedia API error %d: %s", res.StatusCode, string(body))
	}

	var wikiResp WikipediaOnThisDayResponse
	if err := json.Unmarshal(body, &wikiResp); err != nil {
		return nil, err
	}

	var events []string
	for _, event := range wikiResp.Selected {
		events = append(events, fmt.Sprintf("%d: %s", event.Year, event.Text))
	}

	return events, nil
}
