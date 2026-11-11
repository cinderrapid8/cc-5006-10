package media

import (
	"context"
	"encoding/json"
	"os/exec"
)

// ProbeInfo is the subset of ffprobe output we rely on.
type ProbeInfo struct {
	DurationSec float64
	Width       int
	Height      int
	Codec       string
	HasAudio    bool
	// FrameRate as a float when available.
	FrameRate float64
}

type ffprobeJSON struct {
	Streams []struct {
		CodecType  string `json:"codec_type"`
		CodecName  string `json:"codec_name"`
		Width      int    `json:"width"`
		Height     int    `json:"height"`
		AvgFrameRate string `json:"avg_frame_rate"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// Probe runs ffprobe on a media file.
func Probe(ctx context.Context, ffprobe, path string) (ProbeInfo, error) {
	cmd := exec.CommandContext(ctx, ffprobe,
		"-v", "error",
		"-print_format", "json",
		"-show_format", "-show_streams",
		path)
	out, err := cmd.Output()
	if err != nil {
		return ProbeInfo{}, err
	}
	var raw ffprobeJSON
	if err := json.Unmarshal(out, &raw); err != nil {
		return ProbeInfo{}, err
	}
	info := ProbeInfo{}
	for _, s := range raw.Streams {
		switch s.CodecType {
		case "video":
			info.Width = s.Width
			info.Height = s.Height
			info.Codec = s.CodecName
			info.FrameRate = parseRate(s.AvgFrameRate)
		case "audio":
			info.HasAudio = true
		}
	}
	info.DurationSec = parseFloat(raw.Format.Duration)
	return info, nil
}

func parseRate(r string) float64 {
	for i := 0; i < len(r); i++ {
		if r[i] == '/' {
			a := parseFloat(r[:i])
			b := parseFloat(r[i+1:])
			if b == 0 {
				return 0
			}
			return a / b
		}
	}
	return parseFloat(r)
}

func parseFloat(s string) float64 {
	v := 0.0
	neg := false
	i := 0
	if i < len(s) && s[i] == '-' {
		neg = true
		i++
	}
	frac := 0.0
	div := 1.0
	inFrac := false
	for ; i < len(s); i++ {
		c := s[i]
		if c == '.' {
			inFrac = true
			continue
		}
		if c < '0' || c > '9' {
			break
		}
		d := float64(c - '0')
		if inFrac {
			frac = frac*10 + d
			div *= 10
		} else {
			v = v*10 + d
		}
	}
	v += frac / div
	if neg {
		return -v
	}
	return v
}
