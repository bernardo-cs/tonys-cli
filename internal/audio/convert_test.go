package audio

import "testing"

func TestIsAccepted(t *testing.T) {
	cases := []struct {
		ext  string
		want bool
	}{
		{"mp3", true}, {".mp3", true}, {"MP3", true},
		{"flac", true}, {"opus", true}, {"wav", true},
		{"webm", false}, {"amr", false}, {"", false}, {"m4v", false},
	}
	for _, c := range cases {
		if got := IsAccepted(c.ext, nil); got != c.want {
			t.Errorf("IsAccepted(%q) = %v, want %v", c.ext, got, c.want)
		}
	}
	// Custom accepts list overrides the default.
	if IsAccepted("mp3", []string{"wav"}) {
		t.Error("mp3 should not be accepted given accepts=[wav]")
	}
	if !IsAccepted("wav", []string{"wav"}) {
		t.Error("wav should be accepted given accepts=[wav]")
	}
}

func TestCodecFor(t *testing.T) {
	cases := []struct {
		format  string
		wantExt string
		wantErr bool
	}{
		{"", "mp3", false},
		{"mp3", "mp3", false},
		{"opus", "opus", false},
		{"ogg", "ogg", false},
		{"m4a", "m4a", false},
		{"aac", "m4a", false},
		{"flac", "flac", false},
		{"wav", "wav", false},
		{"banana", "", true},
	}
	for _, c := range cases {
		args, ext, err := codecFor(c.format, "128k")
		if (err != nil) != c.wantErr {
			t.Errorf("codecFor(%q) err=%v wantErr=%v", c.format, err, c.wantErr)
			continue
		}
		if !c.wantErr {
			if ext != c.wantExt {
				t.Errorf("codecFor(%q) ext=%q want %q", c.format, ext, c.wantExt)
			}
			if len(args) == 0 {
				t.Errorf("codecFor(%q) returned no args", c.format)
			}
		}
	}
}

func TestExtOf(t *testing.T) {
	if ExtOf("/a/b/song.MP3") != "mp3" {
		t.Errorf("ExtOf failed: %q", ExtOf("/a/b/song.MP3"))
	}
	if ExtOf("noext") != "" {
		t.Errorf("ExtOf(noext) should be empty, got %q", ExtOf("noext"))
	}
}

func TestParseDurationLine(t *testing.T) {
	cases := []struct {
		input   string
		want    float64
		wantOK  bool
	}{
		{"  Duration: 00:03:45.67, start: 0.000, bitrate: 192 kb/s\n", 225.67, true},
		{"  Duration: 01:00:00.00, ...", 3600, true},
		{"  Duration: 00:00:30.500, ...", 30.5, true},
		{"no duration here", 0, false},
	}
	for _, c := range cases {
		got, ok := parseDurationLine(c.input)
		if ok != c.wantOK {
			t.Errorf("parseDurationLine(%q) ok=%v want %v", c.input, ok, c.wantOK)
			continue
		}
		if ok && (got < c.want-0.01 || got > c.want+0.01) {
			t.Errorf("parseDurationLine(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

func TestParseLoudnormJSON(t *testing.T) {
	stderr := `some ffmpeg noise
[Parsed_loudnorm_0 @ 0x]
{
	"input_i" : "-23.45",
	"input_tp" : "-2.10",
	"input_lra" : "7.20",
	"input_thresh" : "-33.65",
	"target_offset" : "0.55"
}
trailing`
	lj, err := parseLoudnormJSON(stderr)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if atof(lj.InputI) != -23.45 || atof(lj.TargetOffset) != 0.55 {
		t.Fatalf("bad parse: %+v", lj)
	}
}
