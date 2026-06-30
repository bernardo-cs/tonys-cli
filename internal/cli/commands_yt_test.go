package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestIsYouTubeRadioURL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://www.youtube.com/watch?v=ml-Q7Njo4YI&list=RDml-Q7Njo4YI&start_radio=1", true},
		{"https://music.youtube.com/watch?v=abc&list=RDabc", true},
		{"https://m.youtube.com/watch?v=abc&start_radio=1", true},
		{"https://www.youtube.com/watch?v=abc", false},
		{"https://www.youtube.com/playlist?list=PL123", false},
		{"https://youtu.be/abc", false},
		{"https://example.com/watch?v=abc&list=RDabc", false},
		{"://bad-url", false},
	}
	for _, c := range cases {
		if got := isYouTubeRadioURL(c.url); got != c.want {
			t.Errorf("isYouTubeRadioURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestParseYouTubeRadioURLCleanURL(t *testing.T) {
	radio := parseYouTubeRadioURL("https://www.youtube.com/watch?v=ml-Q7Njo4YI&list=RDml-Q7Njo4YI&start_radio=1")
	if !radio.isRadio {
		t.Fatal("expected radio URL")
	}
	if radio.cleanURL != "https://www.youtube.com/watch?v=ml-Q7Njo4YI" {
		t.Fatalf("cleanURL = %q", radio.cleanURL)
	}

	playlist := parseYouTubeRadioURL("https://www.youtube.com/playlist?list=PL123")
	if playlist.isRadio || playlist.cleanURL != "" {
		t.Fatalf("playlist parsed as radio: %+v", playlist)
	}
}

func TestAskRadioChoice(t *testing.T) {
	radio := youtubeRadioURL{isRadio: true, cleanURL: "https://www.youtube.com/watch?v=abc"}

	cases := []struct {
		input string
		want  radioChoice
	}{
		{"\n", radioChoiceOne},
		{"one\n", radioChoiceOne},
		{"all\n", radioChoiceAll},
	}
	for _, c := range cases {
		a := &App{Stdin: strings.NewReader(c.input), Stderr: &bytes.Buffer{}}
		got, err := a.askRadioChoice(radio)
		if err != nil {
			t.Fatalf("askRadioChoice(%q): %v", c.input, err)
		}
		if got != c.want {
			t.Fatalf("askRadioChoice(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}
