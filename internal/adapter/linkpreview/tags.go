// Package linkpreview renders the Open Graph and X card tags that the fan web
// serves with an Event page (/events/:id), so social networks and messengers
// can show a preview card without running the app's scripts.
//
// It mirrors internal/adapter/rpc with its mapper: the handler only calls
// usecases and maps entities to an output format, which is an HTML tag block
// here instead of proto.
package linkpreview

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/geo"
)

// maxDescriptionRunes is the upper bound on og:description, in characters.
const maxDescriptionRunes = 100

// defaultImagePath is the brand image served by the fan web when a Series
// has no cover.
const defaultImagePath = "/og-default.png"

// cancelledTitlePrefix marks the preview title of a CANCELLED Series.
const cancelledTitlePrefix = "【公演中止】"

// jst is Japan time, in which every date and time of a preview is written.
var jst = time.FixedZone("JST", 9*60*60)

// jaWeekdays are the Japanese one-character weekday names, Sunday first.
var jaWeekdays = [...]string{"日", "月", "火", "水", "木", "金", "土"}

//go:embed tags.html.tmpl
var tagsTemplateText string

// tagsTemplate escapes every value, so organizer-entered text cannot inject
// markup into the served HTML.
var tagsTemplate = template.Must(template.New("tags").Parse(tagsTemplateText))

// Tags holds the values of one Event page's preview.
type Tags struct {
	// Title is og:title.
	Title string
	// Description is og:description, at most maxDescriptionRunes characters.
	Description string
	// Image is the absolute og:image URL.
	Image string
	// URL is the canonical og:url, without any query parameter.
	URL string
}

// TagBuilder turns a Concert and its Series' Concerts into preview Tags.
type TagBuilder struct {
	siteBaseURL string
	mediaURLs   *mapper.MediaURLBuilder
}

// NewTagBuilder creates a TagBuilder. siteBaseURL is the fan web origin
// (e.g. "https://liverty-music.app"); mediaURLs composes the cover image URL.
func NewTagBuilder(siteBaseURL string, mediaURLs *mapper.MediaURLBuilder) *TagBuilder {
	return &TagBuilder{
		siteBaseURL: strings.TrimRight(siteBaseURL, "/"),
		mediaURLs:   mediaURLs,
	}
}

// Build returns the preview Tags of concert. seriesConcerts are all Concerts
// of its Series in date order, including concert itself; more than one adds
// "全N公演" and the dates to the description.
func (b *TagBuilder) Build(concert *entity.Concert, seriesConcerts []*entity.Concert) Tags {
	return Tags{
		Title:       title(concert),
		Description: description(concert, seriesConcerts),
		Image:       b.image(concert.Series),
		URL:         b.siteBaseURL + "/events/" + concert.ID,
	}
}

// Render writes the escaped tag block of tags.
func Render(tags Tags) ([]byte, error) {
	var buf bytes.Buffer
	if err := tagsTemplate.Execute(&buf, tags); err != nil {
		return nil, fmt.Errorf("render link preview tags: %w", err)
	}
	return buf.Bytes(), nil
}

// title is the Series' title, " | " and the performers' names, prefixed with
// cancelledTitlePrefix when the Series is CANCELLED.
func title(c *entity.Concert) string {
	var sb strings.Builder
	if c.Series.PublishState != nil && *c.Series.PublishState == entity.SeriesPublishStateCancelled {
		sb.WriteString(cancelledTitlePrefix)
	}
	sb.WriteString(c.Series.Title)

	names := make([]string, 0, len(c.Artists))
	for _, p := range c.Artists {
		if p != nil && p.Name != "" {
			names = append(names, p.Name)
		}
	}
	if len(names) > 0 {
		sb.WriteString(" | ")
		sb.WriteString(strings.Join(names, "、"))
	}
	return sb.String()
}

// description is the date with weekday, the doors-open and start times when
// announced and the venue with its prefecture, then "全N公演" with the dates
// when the Series has several Events, then the start of the Series'
// description, truncated to maxDescriptionRunes.
func description(c *entity.Concert, seriesConcerts []*entity.Concert) string {
	parts := []string{longDate(c.LocalDate)}
	if c.OpenTime != nil {
		parts = append(parts, "開場"+c.OpenTime.In(jst).Format("15:04"))
	}
	if c.StartTime != nil {
		parts = append(parts, "開演"+c.StartTime.In(jst).Format("15:04"))
	}
	if v := venue(c.Venue); v != "" {
		parts = append(parts, v)
	}
	if len(seriesConcerts) > 1 {
		dates := make([]string, 0, len(seriesConcerts))
		for _, sc := range seriesConcerts {
			dates = append(dates, shortDate(sc.LocalDate))
		}
		parts = append(parts, fmt.Sprintf("全%d公演 %s", len(seriesConcerts), strings.Join(dates, "・")))
	}
	if d := c.Series.Description; d != nil {
		if text := strings.Join(strings.Fields(*d), " "); text != "" {
			parts = append(parts, text)
		}
	}
	return truncate(strings.Join(parts, " "), maxDescriptionRunes)
}

// venue is the venue name followed by its prefecture in full-width
// parentheses, e.g. "Shibuya WWW（東京都）". The prefecture is omitted when
// unknown or outside Japan.
func venue(v *entity.Venue) string {
	if v == nil || v.Name == "" {
		return ""
	}
	if v.AdminArea != nil {
		if pref, ok := geo.PrefectureName(*v.AdminArea); ok {
			return v.Name + "（" + pref + "）"
		}
	}
	return v.Name
}

// longDate formats a calendar date as "2026年11月20日(金)". LocalDate is a
// calendar date, so it is formatted without a time-zone conversion.
func longDate(d time.Time) string {
	return fmt.Sprintf("%d年%d月%d日(%s)", d.Year(), int(d.Month()), d.Day(), jaWeekdays[d.Weekday()])
}

// shortDate formats a calendar date as "11/20(金)".
func shortDate(d time.Time) string {
	return fmt.Sprintf("%d/%d(%s)", int(d.Month()), d.Day(), jaWeekdays[d.Weekday()])
}

// truncate shortens s to at most limit characters, ending with "…" when cut.
func truncate(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	runes := []rune(s)
	return string(runes[:limit-1]) + "…"
}

// image is the cover's large variant URL, or the brand default image.
func (b *TagBuilder) image(s *entity.Series) string {
	if s.CoverMedia != nil {
		if u := b.mediaURLs.VariantURL(s.CoverMedia.OrganizerID, s.CoverMedia.ID, "large"); u != "" {
			return u
		}
	}
	return b.siteBaseURL + defaultImagePath
}
