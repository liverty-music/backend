package musicbrainz_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/music/musicbrainz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Local types that mirror the unexported url-rels response shapes.
type selectionURLResource struct {
	Resource string `json:"resource"`
}

type selectionURLRelation struct {
	Type         string               `json:"type"`
	SourceCredit string               `json:"source-credit"`
	Ended        bool                 `json:"ended"`
	URL          selectionURLResource `json:"url"`
}

type selectionURLRelsResponse struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Relations []selectionURLRelation `json:"relations"`
}

// homepage builds an official homepage relation.
func homepage(resource, credit string, ended bool) selectionURLRelation {
	return selectionURLRelation{
		Type:         "official homepage",
		SourceCredit: credit,
		Ended:        ended,
		URL:          selectionURLResource{Resource: resource},
	}
}

// resolveFrom serves the given url-rels response from a test server and
// resolves the official site URL through the real client.
func resolveFrom(t *testing.T, artistName string, relations []selectionURLRelation) string {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(selectionURLRelsResponse{
			ID:        "00000000-0000-0000-0000-000000000000",
			Name:      artistName,
			Relations: relations,
		})
	}))
	t.Cleanup(server.Close)

	client := musicbrainz.NewClient(server.Client(), testLogger(t))
	t.Cleanup(func() { _ = client.Close() })
	client.SetBaseURL(server.URL + "/")

	got, err := client.ResolveOfficialSiteURL(context.Background(), "00000000-0000-0000-0000-000000000000")
	require.NoError(t, err)
	return got
}

func TestClient_ResolveOfficialSiteURL_Selection(t *testing.T) {
	t.Parallel()

	const artistName = "Test Artist"

	tests := []struct {
		name      string
		relations []selectionURLRelation
		want      string
	}{
		{
			// @spec components/entity/artist/resolve-official-site-url "Top page preferred over a label page"
			name: "Top page preferred over a label page",
			relations: []selectionURLRelation{
				homepage("https://columbia.jp/artist-info/04limitedsazabys/", "", false),
				homepage("https://www.04limitedsazabys.com/", "", false),
			},
			want: "https://www.04limitedsazabys.com/",
		},
		{
			// @spec components/entity/artist/resolve-official-site-url "Several top pages"
			name: "Several top pages",
			relations: []selectionURLRelation{
				homepage("https://label.example.com/artist/test", "", false),
				homepage("https://first.example.com/", "", false),
				homepage("https://second.example.com", "", false),
			},
			want: "https://first.example.com/",
		},
		{
			// @spec components/entity/artist/resolve-official-site-url "No top page"
			name: "No top page",
			relations: []selectionURLRelation{
				homepage("https://label-a.example.com/artist/test", "Other Name", false),
				homepage("https://label-b.example.com/artist/test", "", false),
				homepage("https://label-c.example.com/artist/test", "", false),
			},
			want: "https://label-b.example.com/artist/test",
		},
		{
			// @spec components/entity/artist/resolve-official-site-url "Link credited to the artist's name"
			name: "Link credited to the artist's name",
			relations: []selectionURLRelation{
				homepage("https://other.example.com/", "Other Name", false),
				homepage("https://uncredited.example.com/", "", false),
				homepage("https://credited.example.com/", "TEST ARTIST", false),
			},
			want: "https://credited.example.com/",
		},
		{
			// @spec components/entity/artist/resolve-official-site-url "Uncredited link"
			name: "Uncredited link",
			relations: []selectionURLRelation{
				homepage("https://other.example.com/", "Other Name", false),
				homepage("https://uncredited.example.com/", "", false),
			},
			want: "https://uncredited.example.com/",
		},
		{
			// @spec components/entity/artist/resolve-official-site-url "Only other credits"
			name: "Only other credits",
			relations: []selectionURLRelation{
				homepage("https://label.example.com/artist/test", "Other Name", false),
				homepage("https://first.example.com/", "Other Name", false),
				homepage("https://second.example.com/", "Another Name", false),
			},
			want: "https://first.example.com/",
		},
		{
			// @spec components/entity/artist/resolve-official-site-url "All links ended"
			name: "All links ended",
			relations: []selectionURLRelation{
				homepage("http://hitsujibungaku.jimdo.com/", "", true),
				homepage("https://old.example.com/", "", true),
			},
			want: "",
		},
		{
			// @spec components/entity/artist/resolve-official-site-url "No homepage link"
			name: "No homepage link",
			relations: []selectionURLRelation{
				{Type: "social network", URL: selectionURLResource{Resource: "https://x.com/test"}},
			},
			want: "",
		},
		{
			name: "ended top page does not hide an active deeper link",
			relations: []selectionURLRelation{
				homepage("https://old.example.com/", "", true),
				homepage("https://label.example.com/artist/test", "", false),
			},
			want: "https://label.example.com/artist/test",
		},
		{
			name: "a link with a query is not a top page",
			relations: []selectionURLRelation{
				homepage("https://example.com/?lang=ja", "", false),
				homepage("https://artist.example.com/", "", false),
			},
			want: "https://artist.example.com/",
		},
		{
			name: "a link with a fragment is not a top page",
			relations: []selectionURLRelation{
				homepage("https://example.com/#top", "", false),
				homepage("https://artist.example.com/", "", false),
			},
			want: "https://artist.example.com/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := resolveFrom(t, artistName, tt.relations)

			assert.Equal(t, tt.want, got)
		})
	}
}

// TestClient_ResolveOfficialSiteURL_FollowedArtistsRegression pins the
// selection for the 30 followed artists that had several active official
// homepage links in MusicBrainz on 2026-10-05. Links are listed in the order
// MusicBrainz returned them and are uncredited unless credited names the link
// credited to the artist's own name (only レキシ has one). The five artists
// stored with a label page then resolve to their own domain; the other 25
// keep the URL the previous rule picked.
func TestClient_ResolveOfficialSiteURL_FollowedArtistsRegression(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		links []string
		// credited is the link credited to the artist's own name, if any.
		credited string
		want     string
	}{
		{name: "レキシ", links: []string{"https://www.jvcmusic.co.jp/-/Artist/A024737.html", "https://www.universal-music.co.jp/rekishi/"}, credited: "https://www.universal-music.co.jp/rekishi/", want: "https://www.universal-music.co.jp/rekishi/"},
		{name: "ヨルシカ", links: []string{"https://www.universal-music.co.jp/yorushika/", "https://yorushika.com/"}, want: "https://yorushika.com/"}, // label page before; own domain now
		{name: "あいみょん", links: []string{"https://wmg.jp/aimyon/", "https://www.aimyong.net/"}, want: "https://www.aimyong.net/"},                  // label page before; own domain now
		{name: "マキシマム ザ ホルモン", links: []string{"http://www.55mth.com/", "http://www.maximumthehormone.jp/"}, want: "http://www.55mth.com/"},
		{name: "サカナクション", links: []string{"http://sakanaction.jp/", "http://www.jvcmusic.co.jp/-/Artist/A020936.html"}, want: "http://sakanaction.jp/"},
		{name: "マカロニえんぴつ", links: []string{"http://macaroniempitsu.com/", "https://www.toysfactory.co.jp/artist/macaroniempitsu"}, want: "http://macaroniempitsu.com/"},
		{name: "04 Limited Sazabys", links: []string{"https://columbia.jp/artist-info/04limitedsazabys/", "https://www.04limitedsazabys.com/", "https://www.nobigdealrecords.jp/artist/04_limited_sazabys.html"}, want: "https://www.04limitedsazabys.com/"}, // label page before; own domain now
		{name: "back number", links: []string{"https://backnumber.info/", "https://www.universal-music.co.jp/backnumber/"}, want: "https://backnumber.info/"},
		{name: "BUMP OF CHICKEN", links: []string{"http://www.bumpofchicken.com/", "http://www.toysfactory.co.jp/artist/bumpofchicken"}, want: "http://www.bumpofchicken.com/"},
		{name: "Busta Rhymes", links: []string{"https://bustarhymesuniverse.com/", "https://mn2s.com/booking-agency/live-roster/busta-rhymes/"}, want: "https://bustarhymesuniverse.com/"},
		{name: "Creepy Nuts", links: []string{"https://creepynuts.com/", "https://www.sonymusic.co.jp/artist/creepynuts/"}, want: "https://creepynuts.com/"},
		{name: "DJ Khaled", links: []string{"https://www.djkhaledofficial.com/", "http://www.djkhaled.org/", "http://www.wethebesttv.com/"}, want: "https://www.djkhaledofficial.com/"},
		{name: "DREAMS COME TRUE", links: []string{"http://dreamscometrue.com/", "http://www.sonymusic.co.jp/Music/Arch/ES/DreamsComeTrue/", "http://www.universal-music.co.jp/dct/", "http://www.virginrecords.com/dreams/"}, want: "http://dreamscometrue.com/"},
		{name: "Galileo Galilei", links: []string{"https://www.galileogalilei.jp/", "https://www.sonymusic.co.jp/Music/Info/galileogalilei/"}, want: "https://www.galileogalilei.jp/"},
		{name: "go!go!vanillas", links: []string{"http://gogovanillas.com/", "https://www.jvcmusic.co.jp/-/Artist/A024836.html"}, want: "http://gogovanillas.com/"},
		{name: "ILLIT", links: []string{"https://beliftlab.com/artist/profile/ILLIT", "https://illit-official.jp/", "https://www.universal-music.co.jp/illit/"}, want: "https://illit-official.jp/"}, // label page before; own domain now
		{name: "Mrs. GREEN APPLE", links: []string{"http://mrsgreenapple.com/", "http://www.universal-music.co.jp/mrsgreenapple/"}, want: "http://mrsgreenapple.com/"},
		{name: "Official髭男dism", links: []string{"https://higedan.com/", "https://www.ponycanyon.co.jp/artist/31450700"}, want: "https://higedan.com/"},
		{name: "Omoinotake", links: []string{"https://omoinotake.com/", "https://www.sonymusic.co.jp/artist/Omoinotake/"}, want: "https://omoinotake.com/"},
		{name: "SEKAI NO OWARI", links: []string{"http://sekainoowari.jp/", "https://endoftheworld.jp/"}, want: "http://sekainoowari.jp/"},
		{name: "SPYAIR", links: []string{"http://www.sonymusic.co.jp/artist/spyair/", "http://www.spyair.net/"}, want: "http://www.spyair.net/"}, // label page before; own domain now
		{name: "SUPER BEAVER", links: []string{"http://www.super-beaver.com/", "http://www.superbeaver.net/"}, want: "http://www.super-beaver.com/"},
		{name: "T.I.", links: []string{"https://www.officialti.com/", "http://www.trapmuzik.com/"}, want: "https://www.officialti.com/"},
		{name: "UNISON SQUARE GARDEN", links: []string{"http://unison-s-g.com/", "http://www.toysfactory.co.jp/artist/usg"}, want: "http://unison-s-g.com/"},
		{name: "Uru", links: []string{"http://uru-official.com/", "http://www.sonymusic.co.jp/artist/uru/"}, want: "http://uru-official.com/"},
		{name: "UVERworld", links: []string{"http://uverworld.jp/", "http://www.sonymusic.co.jp/artist/UVERworld/", "http://www.uverworld.com/"}, want: "http://uverworld.jp/"},
		{name: "東京事変", links: []string{"https://www.tokyojihen.com/", "https://www.universal-music.co.jp/tokyojihen/"}, want: "https://www.tokyojihen.com/"},
		{name: "玉置浩二", links: []string{"http://saltmoderate.com/", "http://www.tamakikoji.jp/"}, want: "http://saltmoderate.com/"},
		{name: "米津玄師", links: []string{"http://reissuerecords.net/", "http://www.sonymusic.co.jp/artist/kenshiyonezu/"}, want: "http://reissuerecords.net/"},
		{name: "緑黄色社会", links: []string{"http://www.ryokushaka.com/", "http://www.sonymusic.co.jp/artist/ryokusyaka/"}, want: "http://www.ryokushaka.com/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			relations := make([]selectionURLRelation, 0, len(tt.links))
			for _, link := range tt.links {
				credit := ""
				if link == tt.credited {
					credit = tt.name
				}
				relations = append(relations, homepage(link, credit, false))
			}

			got := resolveFrom(t, tt.name, relations)

			assert.Equal(t, tt.want, got)
		})
	}
}
