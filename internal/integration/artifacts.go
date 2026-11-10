package integration

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"xiaoya-media-server/internal/embyserver"
	"xiaoya-media-server/internal/metadata/tmdb"
	"xiaoya-media-server/internal/resource/discovery"
	"xiaoya-media-server/internal/strm"
)

type movieNFO struct {
	XMLName       xml.Name `xml:"movie"`
	Title         string   `xml:"title,omitempty"`
	OriginalTitle string   `xml:"originaltitle,omitempty"`
	Plot          string   `xml:"plot,omitempty"`
	Tagline       string   `xml:"tagline,omitempty"`
	Premiered     string   `xml:"premiered,omitempty"`
	Year          int      `xml:"year,omitempty"`
	Rating        float64  `xml:"rating,omitempty"`
	Runtime       float64  `xml:"runtime,omitempty"`
	UniqueID      *nfoID   `xml:"uniqueid,omitempty"`
}

type nfoID struct {
	Type    string `xml:"type,attr"`
	Default bool   `xml:"default,attr,omitempty"`
	Value   string `xml:",chardata"`
}

type tvshowNFO struct {
	XMLName       xml.Name `xml:"tvshow"`
	Title         string   `xml:"title,omitempty"`
	OriginalTitle string   `xml:"originaltitle,omitempty"`
	Plot          string   `xml:"plot,omitempty"`
	Premiered     string   `xml:"premiered,omitempty"`
	Year          int      `xml:"year,omitempty"`
	Rating        float64  `xml:"rating,omitempty"`
	UniqueID      *nfoID   `xml:"uniqueid,omitempty"`
}

type seasonNFO struct {
	XMLName xml.Name `xml:"season"`
	Title   string   `xml:"title,omitempty"`
	Season  int      `xml:"seasonnumber"`
}

type episodeNFO struct {
	XMLName   xml.Name `xml:"episodedetails"`
	Title     string   `xml:"title,omitempty"`
	Plot      string   `xml:"plot,omitempty"`
	Premiered string   `xml:"aired,omitempty"`
	Season    int      `xml:"season"`
	Episode   int      `xml:"episode"`
	Rating    float64  `xml:"rating,omitempty"`
	Runtime   float64  `xml:"runtime,omitempty"`
	UniqueID  *nfoID   `xml:"uniqueid,omitempty"`
}

func marshalNFO(v any) ([]byte, error) {
	payload, err := xml.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n"), payload...), nil
}

func writeArtifact(target string, payload []byte) error {
	_, _, err := strm.WriteAtomicIdempotent(filepath.Dir(target), target, payload)
	return err
}

func downloadImage(ctx context.Context, rawURL string) ([]byte, error) {
	if strings.TrimSpace(rawURL) == "" {
		return nil, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("image HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, errors.New("empty image response")
	}
	return body, nil
}

func writeImageIfMissing(ctx context.Context, target, rawURL string) error {
	if strings.TrimSpace(rawURL) == "" {
		return nil
	}
	if _, err := os.Stat(target); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	body, err := downloadImage(ctx, rawURL)
	if err != nil {
		return err
	}
	return writeArtifact(target, body)
}

func (p *Pipeline) publishMatchedArtifacts(ctx context.Context, res discovery.MediaResource, identity discovery.ParsedMediaIdentity, series, season, episode *tmdb.MediaMetadata) error {
	path := strings.TrimSpace(res.LocalSTRMPath)
	if path == "" {
		return nil
	}
	if identity.MediaType == "MOVIE" {
		if series == nil {
			return errors.New("movie metadata is empty")
		}
		payload, err := marshalNFO(movieNFO{Title: series.Title, OriginalTitle: series.OriginalTitle, Plot: series.Overview, Tagline: series.Tagline, Premiered: series.Premiered, Year: series.Year, Rating: series.Rating, Runtime: series.RuntimeMin, UniqueID: &nfoID{Type: "tmdb", Default: true, Value: series.TMDBID}})
		if err != nil {
			return err
		}
		base := strings.TrimSuffix(path, filepath.Ext(path))
		if err := writeArtifact(base+".nfo", payload); err != nil {
			return err
		}
		if err := writeArtifact(filepath.Join(filepath.Dir(path), "movie.nfo"), payload); err != nil {
			return err
		}
		if err := writeImageIfMissing(ctx, base+"-poster.jpg", series.PosterURL); err != nil {
			return err
		}
		if err := writeImageIfMissing(ctx, base+"-fanart.jpg", series.BackdropURL); err != nil {
			return err
		}
		if p.app != nil {
			return p.app.PublishDirectMedia(embyserver.DirectMediaPublication{
				MediaType:     "MOVIE",
				LocalSTRM:     path,
				StreamURL:     res.SourceContent,
				Title:         series.Title,
				OriginalTitle: series.OriginalTitle,
				Year:          series.Year,
				Rating:        series.Rating,
				Overview:      series.Overview,
				Tagline:       series.Tagline,
				Premiered:     series.Premiered,
				RuntimeMin:    series.RuntimeMin,
				TMDBID:        series.TMDBID,
				PosterPath:    base + "-poster.jpg",
				BackdropPath:  base + "-fanart.jpg",
			})
		}
		return nil
	}

	if series == nil {
		return errors.New("series metadata is empty")
	}
	seriesDir := filepath.Dir(filepath.Dir(path))
	seriesPayload, err := marshalNFO(tvshowNFO{Title: series.Title, OriginalTitle: series.OriginalTitle, Plot: series.Overview, Premiered: series.Premiered, Year: series.Year, Rating: series.Rating, UniqueID: &nfoID{Type: "tmdb", Default: true, Value: series.TMDBID}})
	if err != nil {
		return err
	}
	if err := writeArtifact(filepath.Join(seriesDir, "tvshow.nfo"), seriesPayload); err != nil {
		return err
	}
	if err := writeImageIfMissing(ctx, filepath.Join(seriesDir, "poster.jpg"), series.PosterURL); err != nil {
		return err
	}
	if err := writeImageIfMissing(ctx, filepath.Join(seriesDir, "fanart.jpg"), series.BackdropURL); err != nil {
		return err
	}
	seasonDir := filepath.Dir(path)
	seasonNumber := identity.SeasonNumber
	if seasonNumber < 0 {
		seasonNumber = 0
	}
	seasonTitle := fmt.Sprintf("Season %d", seasonNumber)
	if season != nil && season.Title != "" {
		seasonTitle = season.Title
	}
	seasonPayload, err := marshalNFO(seasonNFO{Title: seasonTitle, Season: seasonNumber})
	if err != nil {
		return err
	}
	if err := writeArtifact(filepath.Join(seasonDir, "season.nfo"), seasonPayload); err != nil {
		return err
	}
	ep := episodeNFO{Title: fmt.Sprintf("第 %d 集", identity.EpisodeNumber), Season: seasonNumber, Episode: identity.EpisodeNumber, UniqueID: &nfoID{Type: "tmdb", Value: series.TMDBID}}
	if episode != nil {
		ep.Title, ep.Plot, ep.Premiered, ep.Rating, ep.Runtime = episode.Title, episode.Overview, episode.Premiered, episode.Rating, episode.RuntimeMin
	}
	episodePayload, err := marshalNFO(ep)
	if err != nil {
		return err
	}
	base := strings.TrimSuffix(path, filepath.Ext(path))
	if err := writeArtifact(base+".nfo", episodePayload); err != nil {
		return err
	}
	if episode != nil {
		if err := writeImageIfMissing(ctx, base+"-thumb.jpg", episode.ThumbURL); err != nil {
			return err
		}
	}
	if p.app != nil {
		epTitle := fmt.Sprintf("第 %d 集", identity.EpisodeNumber)
		epPlot := ""
		epThumb := ""
		if episode != nil {
			if episode.Title != "" {
				epTitle = episode.Title
			}
			epPlot = episode.Overview
			epThumb = base + "-thumb.jpg"
		}
		return p.app.PublishDirectMedia(embyserver.DirectMediaPublication{
			MediaType:     "EPISODE",
			LocalSTRM:     path,
			StreamURL:     res.SourceContent,
			Title:         series.Title,
			OriginalTitle: series.OriginalTitle,
			Year:          series.Year,
			Rating:        series.Rating,
			Overview:      series.Overview,
			Premiered:     series.Premiered,
			TMDBID:        series.TMDBID,
			PosterPath:    filepath.Join(seriesDir, "poster.jpg"),
			BackdropPath:  filepath.Join(seriesDir, "fanart.jpg"),
			SeasonNumber:  seasonNumber,
			EpisodeNumber: identity.EpisodeNumber,
			EpisodeTitle:  epTitle,
			EpisodePlot:   epPlot,
			EpisodeThumb:  epThumb,
		})
	}
	return nil
}
