// Package toniecloud is a Go client for the TonieCloud REST API
// (https://api.tonie.cloud/v2). It is a faithful, feature-complete port of the
// Python library github.com/alexhartm/tonie_api, extended with a real
// token-caching auth layer, structured errors, single-tonie reads and
// chapter-level editing.
//
// It is NOT associated with Boxine / tonies.de in any way.
package toniecloud

import (
	"encoding/json"
	"time"
)

// User is the account behind a set of credentials (GET /me).
//
// The TonieCloud API returns many more fields than the upstream Python library
// models; we keep the documented essentials as typed fields and preserve
// everything else (firstName, locale, country, region, ...) in Extra so no data
// is lost and agents can read arbitrary attributes.
type User struct {
	UUID  string `json:"uuid"`
	Email string `json:"email"`

	// Extra holds every other field returned by /me verbatim.
	Extra map[string]any `json:"-"`
}

// UnmarshalJSON keeps the typed uuid/email fields while preserving every other
// attribute the API returns in Extra.
func (u *User) UnmarshalJSON(b []byte) error {
	var all map[string]any
	if err := json.Unmarshal(b, &all); err != nil {
		return err
	}
	if v, ok := all["uuid"].(string); ok {
		u.UUID = v
	}
	if v, ok := all["email"].(string); ok {
		u.Email = v
	}
	delete(all, "uuid")
	delete(all, "email")
	u.Extra = all
	return nil
}

// MarshalJSON re-flattens Extra back alongside the typed fields.
func (u User) MarshalJSON() ([]byte, error) {
	out := map[string]any{}
	for k, v := range u.Extra {
		out[k] = v
	}
	out["uuid"] = u.UUID
	out["email"] = u.Email
	return json.Marshal(out)
}

// Config is the backend configuration and upload limits (GET /config).
type Config struct {
	Locales        []string `json:"locales"`
	UnicodeLocales []string `json:"unicodeLocales"`
	MaxChapters    int      `json:"maxChapters"`
	MaxSeconds     int      `json:"maxSeconds"`
	MaxBytes       int64    `json:"maxBytes"`
	Accepts        []string `json:"accepts"`
	StageWarning   bool     `json:"stageWarning"`
	PaypalClientID string   `json:"paypalClientId"`
	SSOEnabled     bool     `json:"ssoEnabled"`
}

// Household groups creative tonies and is the unit of access control
// (GET /households).
type Household struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	OwnerName string `json:"ownerName"`
	Access    string `json:"access"`
	CanLeave  bool   `json:"canLeave"`
	Image     string `json:"image,omitempty"`

	// ForeignCreativeTonieContent indicates whether content from other
	// households may be played on this household's tonies.
	ForeignCreativeTonieContent bool `json:"foreignCreativeTonieContent,omitempty"`
}

// Chapter is a single audio track on a creative tonie.
type Chapter struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// File is the file identifier. For new chapters this is the UUID returned
	// by POST /file. For existing chapters it is an opaque blob; for content
	// shipped by tonies it starts with "ContentToken:".
	File        string  `json:"file"`
	Seconds     float64 `json:"seconds"`
	Transcoding bool    `json:"transcoding"`
}

// IsContentToken reports whether the chapter is tonies-published content rather
// than a user upload. Such chapters cannot be re-uploaded, only reordered or
// removed.
func (c Chapter) IsContentToken() bool {
	const p = "ContentToken:"
	return len(c.File) >= len(p) && c.File[:len(p)] == p
}

// CreativeTonie is a re-recordable tonie figurine
// (GET /households/{id}/creativetonies).
type CreativeTonie struct {
	ID                string     `json:"id"`
	HouseholdID       string     `json:"householdId"`
	Name              string     `json:"name"`
	ImageURL          string     `json:"imageUrl"`
	SecondsRemaining  float64    `json:"secondsRemaining"`
	SecondsPresent    float64    `json:"secondsPresent"`
	ChaptersRemaining int        `json:"chaptersRemaining"`
	ChaptersPresent   int        `json:"chaptersPresent"`
	Transcoding       bool       `json:"transcoding"`
	Live              bool       `json:"live"`
	Private           bool       `json:"private"`
	LastUpdate        *time.Time `json:"lastUpdate"`
	TranscodingErrors []any      `json:"transcodingErrors,omitempty"`
	Chapters          []Chapter  `json:"chapters"`
}

// Request is the presigned S3 POST returned inside a FileUploadRequest.
type Request struct {
	URL    string            `json:"url"`
	Fields map[string]string `json:"fields"`
}

// FileUploadRequest is returned by POST /file and contains a presigned S3 form
// plus the fileId to reference once the upload completes.
type FileUploadRequest struct {
	Request Request `json:"request"`
	FileID  string  `json:"fileId"`
}
