package domain

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
)

// ParseClock parses a record-clock expression.
// Accepted forms: "45:10.5", "45:10", "90+3:12", "2710.5" (plain seconds).
// Result is a number of seconds on a monotonically increasing record clock.
// Stoppage time (90+3:00) is flattened to 93:00 so the clock stays monotonic.
func ParseClock(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty clock value")
	}
	if !strings.ContainsAny(s, ":+") {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, errors.New("invalid clock: " + s)
		}
		return v, nil
	}

	plus := 0.0
	if i := strings.Index(s, "+"); i >= 0 {
		rest := s[i+1:]
		// Stoppage form "90+3:12": minutes before the colon, seconds after.
		if c := strings.Index(rest, ":"); c >= 0 {
			p, err := strconv.ParseFloat(strings.TrimSpace(rest[:c]), 64)
			if err != nil {
				return 0, errors.New("invalid stoppage time in: " + s)
			}
			plus = p * 60
			s = s[:i] + rest[c:]
		} else {
			p, err := strconv.ParseFloat(strings.TrimSpace(rest), 64)
			if err != nil {
				return 0, errors.New("invalid stoppage time in: " + s)
			}
			plus = p
			s = s[:i]
		}
	}

	parts := strings.Split(s, ":")
	if len(parts) > 3 {
		return 0, errors.New("invalid clock: " + s)
	}
	var total float64
	mult := 1.0
	for j := len(parts) - 1; j >= 0; j-- {
		v, err := strconv.ParseFloat(strings.TrimSpace(parts[j]), 64)
		if err != nil {
			return 0, errors.New("invalid clock component in: " + s)
		}
		total += v * mult
		mult *= 60
	}
	return total + plus, nil
}

// FormatClock renders seconds as mm:ss.s for display.
func FormatClock(sec float64) string {
	if sec < 0 {
		return "-" + FormatClock(-sec)
	}
	m := math.Floor(sec / 60)
	r := sec - m*60
	return strconv.Itoa(int(m)) + ":" + strconv.FormatFloat(r, 'f', 1, 64)
}

// FormatDuration renders a duration in seconds as HH:MM:SS or MM:SS.
func FormatDuration(sec float64) string {
	d := time.Duration(math.Round(sec*1000)) * time.Millisecond
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return strconv.Itoa(h) + ":" + twoDigits(m) + ":" + twoDigits(s)
	}
	return twoDigits(m) + ":" + twoDigits(s)
}

func twoDigits(v int) string {
	if v < 10 {
		return "0" + strconv.Itoa(v)
	}
	return strconv.Itoa(v)
}
