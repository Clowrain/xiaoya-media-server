package embyserver

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

//go:embed assets/font.ttf
var defaultEmbeddedFont []byte

var (
	vectorFontOnce   sync.Once
	vectorParsedFont *sfnt.Font
)

func getVectorFont() *sfnt.Font {
	vectorFontOnce.Do(func() {
		// 1. 尝试从环境变量或常用挂载/系统路径探测中文字体
		candidates := []string{
			os.Getenv("XMS_FONT_PATH"),
			"/data/font.ttf",
			"/data/font.ttc",
			"/etc/xiaoya-media-server/font.ttf",
			"/usr/share/fonts/wqy-zenhei/wqy-zenhei.ttc",
			"/usr/share/fonts/truetype/wqy/wqy-zenhei.ttc",
			"/usr/share/fonts/truetype/wqy/wqy-microhei.ttc",
			"/usr/share/fonts/noto-cjk/NotoSansCJK-Regular.ttc",
			"/System/Library/Fonts/PingFang.ttc",
			"/System/Library/Fonts/STHeiti Light.ttc",
		}
		for _, fp := range candidates {
			if fp == "" {
				continue
			}
			if b, err := os.ReadFile(fp); err == nil && len(b) > 0 {
				if f, err := opentype.Parse(b); err == nil {
					vectorParsedFont = f
					return
				}
				if coll, err := opentype.ParseCollection(b); err == nil && coll.NumFonts() > 0 {
					if f, err := coll.Font(0); err == nil {
						vectorParsedFont = f
						return
					}
				}
			}
		}
		// 2. 加载内置嵌入的开源中文字体 (Smiley Sans / 得意黑)
		if len(defaultEmbeddedFont) > 0 {
			if f, err := opentype.Parse(defaultEmbeddedFont); err == nil {
				vectorParsedFont = f
			}
		}
	})
	return vectorParsedFont
}

func drawVectorText(img *image.RGBA, text string, x, y int, fontSize float64, col color.RGBA) bool {
	f := getVectorFont()
	if f == nil {
		return false
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{
		Size:    fontSize,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return false
	}
	defer face.Close()

	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(col),
		Face: face,
		Dot:  fixed.Point26_6{X: fixed.I(x), Y: fixed.I(y)},
	}
	d.DrawString(text)
	return true
}

func measureVectorText(text string, fontSize float64) int {
	f := getVectorFont()
	if f == nil {
		return 0
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{
		Size:    fontSize,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return 0
	}
	defer face.Close()

	d := &font.Drawer{
		Face: face,
	}
	return d.MeasureString(text).Ceil()
}

func drawCenteredText(img *image.RGBA, text string, centerY int, fontSize float64, col color.RGBA) {
	bounds := img.Bounds()
	w := bounds.Dx()
	wPx := measureVectorText(text, fontSize)
	if wPx > 0 {
		if wPx > w-32 && fontSize > 16 {
			fontSize = fontSize * float64(w-32) / float64(wPx)
			if fontSize < 14 {
				fontSize = 14
			}
			wPx = measureVectorText(text, fontSize)
		}
		startX := (w - wPx) / 2
		baselineY := centerY + int(fontSize*0.35)
		drawVectorText(img, text, startX, baselineY, fontSize, col)
		return
	}

	scale := int(fontSize / 8)
	if scale < 1 {
		scale = 1
	}
	tw := textWidth(text, scale)
	drawText(img, text, w/2-tw/2, centerY-scale*3, scale, col)
}

// Preset themes: background gradients (top -> bottom), accent highlight colors
type posterPalette struct {
	topBg   color.RGBA
	botBg   color.RGBA
	accent  color.RGBA
	subText color.RGBA
}

var posterPalettes = []posterPalette{
	// 0: Deep Twilight (Midnight Blue)
	{color.RGBA{22, 34, 60, 255}, color.RGBA{6, 10, 20, 255}, color.RGBA{56, 189, 248, 255}, color.RGBA{148, 163, 184, 255}},
	// 1: Burgundy Velvet
	{color.RGBA{60, 20, 32, 255}, color.RGBA{18, 5, 10, 255}, color.RGBA{251, 113, 133, 255}, color.RGBA{225, 175, 185, 255}},
	// 2: Emerald Nebula
	{color.RGBA{14, 48, 34, 255}, color.RGBA{4, 16, 12, 255}, color.RGBA{52, 211, 153, 255}, color.RGBA{167, 243, 208, 255}},
	// 3: Royal Amethyst
	{color.RGBA{48, 22, 68, 255}, color.RGBA{14, 6, 22, 255}, color.RGBA{192, 132, 252, 255}, color.RGBA{216, 180, 254, 255}},
	// 4: Golden Amber
	{color.RGBA{54, 34, 14, 255}, color.RGBA{18, 11, 5, 255}, color.RGBA{251, 191, 36, 255}, color.RGBA{253, 230, 138, 255}},
	// 5: Obsidian Slate
	{color.RGBA{36, 40, 48, 255}, color.RGBA{12, 13, 16, 255}, color.RGBA{148, 163, 184, 255}, color.RGBA{203, 213, 225, 255}},
	// 6: Deep Teal Ocean
	{color.RGBA{15, 45, 56, 255}, color.RGBA{5, 16, 20, 255}, color.RGBA{45, 212, 191, 255}, color.RGBA{153, 246, 228, 255}},
	// 7: Crimson Dusk
	{color.RGBA{58, 18, 40, 255}, color.RGBA{16, 5, 12, 255}, color.RGBA{244, 114, 182, 255}, color.RGBA{251, 207, 232, 255}},
}

func pickPalette(seed string) posterPalette {
	h := md5.Sum([]byte(seed))
	idx := int(h[0]) % len(posterPalettes)
	return posterPalettes[idx]
}

// 5x7 Basic Bitmap Font for ASCII (Space, 0-9, A-Z, -, :)
var font5x7 = map[rune][7]uint8{
	' ': {0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
	'-': {0x00, 0x00, 0x00, 0x1F, 0x00, 0x00, 0x00},
	':': {0x00, 0x0C, 0x0C, 0x00, 0x0C, 0x0C, 0x00},
	'0': {0x0E, 0x11, 0x13, 0x15, 0x19, 0x11, 0x0E},
	'1': {0x04, 0x0C, 0x04, 0x04, 0x04, 0x04, 0x0E},
	'2': {0x0E, 0x11, 0x01, 0x06, 0x08, 0x10, 0x1F},
	'3': {0x1F, 0x02, 0x04, 0x02, 0x01, 0x11, 0x0E},
	'4': {0x02, 0x06, 0x0A, 0x12, 0x1F, 0x02, 0x02},
	'5': {0x1F, 0x10, 0x1E, 0x01, 0x01, 0x11, 0x0E},
	'6': {0x06, 0x08, 0x10, 0x1E, 0x11, 0x11, 0x0E},
	'7': {0x1F, 0x01, 0x02, 0x04, 0x08, 0x08, 0x08},
	'8': {0x0E, 0x11, 0x11, 0x0E, 0x11, 0x11, 0x0E},
	'9': {0x0E, 0x11, 0x11, 0x0F, 0x01, 0x02, 0x0C},
	'A': {0x0E, 0x11, 0x11, 0x1F, 0x11, 0x11, 0x11},
	'B': {0x1E, 0x11, 0x11, 0x1E, 0x11, 0x11, 0x1E},
	'C': {0x0E, 0x11, 0x10, 0x10, 0x10, 0x11, 0x0E},
	'D': {0x1C, 0x12, 0x11, 0x11, 0x11, 0x12, 0x1C},
	'E': {0x1F, 0x10, 0x10, 0x1E, 0x10, 0x10, 0x1F},
	'F': {0x1F, 0x10, 0x10, 0x1E, 0x10, 0x10, 0x10},
	'G': {0x0E, 0x11, 0x10, 0x17, 0x11, 0x11, 0x0F},
	'H': {0x11, 0x11, 0x11, 0x1F, 0x11, 0x11, 0x11},
	'I': {0x0E, 0x04, 0x04, 0x04, 0x04, 0x04, 0x0E},
	'J': {0x07, 0x02, 0x02, 0x02, 0x02, 0x12, 0x0C},
	'K': {0x11, 0x12, 0x14, 0x18, 0x14, 0x12, 0x11},
	'L': {0x10, 0x10, 0x10, 0x10, 0x10, 0x10, 0x1F},
	'M': {0x11, 0x1B, 0x15, 0x15, 0x11, 0x11, 0x11},
	'N': {0x11, 0x19, 0x15, 0x13, 0x11, 0x11, 0x11},
	'O': {0x0E, 0x11, 0x11, 0x11, 0x11, 0x11, 0x0E},
	'P': {0x1E, 0x11, 0x11, 0x1E, 0x10, 0x10, 0x10},
	'Q': {0x0E, 0x11, 0x11, 0x11, 0x15, 0x12, 0x0D},
	'R': {0x1E, 0x11, 0x11, 0x1E, 0x14, 0x12, 0x11},
	'S': {0x0E, 0x11, 0x10, 0x0E, 0x01, 0x11, 0x0E},
	'T': {0x1F, 0x04, 0x04, 0x04, 0x04, 0x04, 0x04},
	'U': {0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x0E},
	'V': {0x11, 0x11, 0x11, 0x11, 0x11, 0x0A, 0x04},
	'W': {0x11, 0x11, 0x11, 0x15, 0x15, 0x15, 0x0A},
	'X': {0x11, 0x11, 0x0A, 0x04, 0x0A, 0x11, 0x11},
	'Y': {0x11, 0x11, 0x0A, 0x04, 0x04, 0x04, 0x04},
	'Z': {0x1F, 0x01, 0x02, 0x04, 0x08, 0x10, 0x1F},
}

// drawText renders uppercase ASCII text at (startX, startY) with integer scale
func drawText(img *image.RGBA, text string, startX, startY, scale int, c color.RGBA) {
	upper := strings.ToUpper(text)
	curX := startX
	for _, ch := range upper {
		matrix, ok := font5x7[ch]
		if !ok {
			matrix = font5x7[' ']
		}
		for row := 0; row < 7; row++ {
			bits := matrix[row]
			for col := 0; col < 5; col++ {
				if (bits & (1 << (4 - col))) != 0 {
					for dy := 0; dy < scale; dy++ {
						for dx := 0; dx < scale; dx++ {
							px := curX + col*scale + dx
							py := startY + row*scale + dy
							if px >= 0 && px < img.Bounds().Dx() && py >= 0 && py < img.Bounds().Dy() {
								img.SetRGBA(px, py, c)
							}
						}
					}
				}
			}
		}
		curX += (5 + 1) * scale
	}
}

// textWidth computes width in pixels of scaled 5x7 string
func textWidth(text string, scale int) int {
	if len(text) == 0 {
		return 0
	}
	return (len(text)*6 - 1) * scale
}

// fillGradient renders vertical gradient with spotlight radial halo
func fillGradient(img *image.RGBA, pal posterPalette) {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	cx, cy := float64(w)/2.0, float64(h)*0.35
	maxRadius := float64(w) * 0.75

	for y := 0; y < h; y++ {
		t := float64(y) / float64(h)
		baseR := float64(pal.topBg.R)*(1.0-t) + float64(pal.botBg.R)*t
		baseG := float64(pal.topBg.G)*(1.0-t) + float64(pal.botBg.G)*t
		baseB := float64(pal.topBg.B)*(1.0-t) + float64(pal.botBg.B)*t

		for x := 0; x < w; x++ {
			dx := float64(x) - cx
			dy := float64(y) - cy
			dist := math.Sqrt(dx*dx + dy*dy)
			halo := 0.0
			if dist < maxRadius {
				halo = (1.0 - dist/maxRadius) * 0.35
			}
			r := uint8(math.Min(255, baseR+float64(pal.accent.R)*halo))
			g := uint8(math.Min(255, baseG+float64(pal.accent.G)*halo))
			b := uint8(math.Min(255, baseB+float64(pal.accent.B)*halo))
			img.SetRGBA(x, y, color.RGBA{r, g, b, 255})
		}
	}

	// 1px subtle border inside
	borderCol := color.RGBA{pal.accent.R / 4, pal.accent.G / 4, pal.accent.B / 4, 180}
	for x := 0; x < w; x++ {
		img.SetRGBA(x, 0, borderCol)
		img.SetRGBA(x, h-1, borderCol)
	}
	for y := 0; y < h; y++ {
		img.SetRGBA(0, y, borderCol)
		img.SetRGBA(w-1, y, borderCol)
	}
}

// drawBadge draws a rounded pill tag at (cx, y)
func drawBadge(img *image.RGBA, text string, cx, y, scale int, textCol, bgCol color.RGBA) {
	tw := textWidth(text, scale)
	padX, padY := 12, 6
	bw := tw + padX*2
	bh := 7*scale + padY*2
	bx := cx - bw/2
	by := y

	// Draw rounded box
	for py := by; py < by+bh; py++ {
		for px := bx; px < bx+bw; px++ {
			if px >= 0 && px < img.Bounds().Dx() && py >= 0 && py < img.Bounds().Dy() {
				// corners
				corner := false
				if (px == bx || px == bx+bw-1) && (py == by || py == by+bh-1) {
					corner = true
				}
				if !corner {
					img.SetRGBA(px, py, bgCol)
				}
			}
		}
	}
	drawText(img, text, bx+padX, by+padY, scale, textCol)
}

// drawClapperboard draws a geometric clapperboard icon (Movie)
func drawClapperboard(img *image.RGBA, cx, cy int, accent color.RGBA) {
	w, h := 100, 80
	x0, y0 := cx-w/2, cy-h/2
	white := color.RGBA{240, 240, 245, 240}
	dark := color.RGBA{25, 25, 30, 240}

	// Bottom board body
	for y := y0 + 26; y < y0+h; y++ {
		for x := x0; x < x0+w; x++ {
			img.SetRGBA(x, y, dark)
		}
	}
	// Accent lines inside bottom board
	for y := y0 + 44; y <= y0+46; y++ {
		for x := x0 + 10; x < x0+w-10; x++ {
			img.SetRGBA(x, y, accent)
		}
	}
	for y := y0 + 60; y <= y0+62; y++ {
		for x := x0 + 10; x < x0+w-30; x++ {
			img.SetRGBA(x, y, color.RGBA{accent.R / 2, accent.G / 2, accent.B / 2, 200})
		}
	}

	// Top hinged stick with diagonal stripes
	for y := y0; y < y0+22; y++ {
		for x := x0; x < x0+w; x++ {
			stripe := ((x - x0) + (y - y0)) / 14
			if stripe%2 == 0 {
				img.SetRGBA(x, y, white)
			} else {
				img.SetRGBA(x, y, dark)
			}
		}
	}
}

// drawTVIcon draws retro TV monitor (Series / TV)
func drawTVIcon(img *image.RGBA, cx, cy int, accent color.RGBA) {
	w, h := 108, 80
	x0, y0 := cx-w/2, cy-h/2
	tvBg := color.RGBA{30, 32, 40, 255}
	screenBg := color.RGBA{15, 20, 28, 255}

	// Antennas
	for i := 0; i < 24; i++ {
		// left antenna
		img.SetRGBA(cx-10-i, y0-i/2, accent)
		img.SetRGBA(cx-10-i, y0-i/2-1, accent)
		// right antenna
		img.SetRGBA(cx+10+i, y0-i/2, accent)
		img.SetRGBA(cx+10+i, y0-i/2-1, accent)
	}

	// Outer TV chassis
	for y := y0; y < y0+h; y++ {
		for x := x0; x < x0+w; x++ {
			img.SetRGBA(x, y, tvBg)
		}
	}

	// Screen
	sw, sh := 74, 60
	sx0, sy0 := x0+8, y0+10
	for y := sy0; y < sy0+sh; y++ {
		for x := sx0; x < sx0+sw; x++ {
			img.SetRGBA(x, y, screenBg)
		}
	}
	// Play icon on screen
	for dy := -12; dy <= 12; dy++ {
		span := (12 - int(math.Abs(float64(dy)))) * 4 / 3
		for dx := 0; dx <= span; dx++ {
			img.SetRGBA(sx0+sw/2-6+dx, sy0+sh/2+dy, accent)
		}
	}

	// Dials on right panel
	knobColor := color.RGBA{accent.R, accent.G, accent.B, 220}
	for dy := -4; dy <= 4; dy++ {
		for dx := -4; dx <= 4; dx++ {
			if dx*dx+dy*dy <= 16 {
				img.SetRGBA(x0+94+dx, y0+24+dy, knobColor)
				img.SetRGBA(x0+94+dx, y0+46+dy, knobColor)
			}
		}
	}
}

// drawPlayReelIcon draws episode / film reel symbol (Episode)
func drawPlayReelIcon(img *image.RGBA, cx, cy int, accent color.RGBA) {
	w, h := 96, 68
	x0, y0 := cx-w/2, cy-h/2
	frameBg := color.RGBA{24, 28, 36, 255}
	innerBg := color.RGBA{12, 14, 18, 255}

	// Film frame box
	for y := y0; y < y0+h; y++ {
		for x := x0; x < x0+w; x++ {
			img.SetRGBA(x, y, frameBg)
		}
	}
	for y := y0 + 6; y < y0+h-6; y++ {
		for x := x0 + 14; x < x0+w-14; x++ {
			img.SetRGBA(x, y, innerBg)
		}
	}
	// Sprocket holes
	holeCol := color.RGBA{8, 10, 14, 255}
	for _, sy := range []int{y0 + 10, y0 + 26, y0 + 42} {
		for py := sy; py < sy+10; py++ {
			for px := x0 + 4; px < x0+10; px++ {
				img.SetRGBA(px, py, holeCol)
			}
			for px := x0 + w - 10; px < x0+w-4; px++ {
				img.SetRGBA(px, py, holeCol)
			}
		}
	}
	// Play triangle
	for dy := -14; dy <= 14; dy++ {
		span := (14 - int(math.Abs(float64(dy)))) * 4 / 3
		for dx := 0; dx <= span; dx++ {
			img.SetRGBA(cx-8+dx, cy+dy, accent)
		}
	}
}

// drawLibraryCollageIcon draws layered collection folders (Library)
func drawLibraryCollageIcon(img *image.RGBA, cx, cy int, accent color.RGBA) {
	w, h := 88, 64
	rearX, rearY := cx-w/2+10, cy-h/2-8
	foreX, foreY := cx-w/2-6, cy-h/2+6

	// Rear folder card
	rearBg := color.RGBA{accent.R / 3, accent.G / 3, accent.B / 3, 160}
	for y := rearY; y < rearY+h; y++ {
		for x := rearX; x < rearX+w; x++ {
			img.SetRGBA(x, y, rearBg)
		}
	}

	// Front folder card
	foreBg := color.RGBA{30, 36, 48, 240}
	for y := foreY; y < foreY+h; y++ {
		for x := foreX; x < foreX+w; x++ {
			img.SetRGBA(x, y, foreBg)
		}
	}
	// Front card accent header tab
	for y := foreY; y < foreY+12; y++ {
		for x := foreX; x < foreX+w/2; x++ {
			img.SetRGBA(x, y, accent)
		}
	}
	// Grid icons inside front card
	for gy := 0; gy < 2; gy++ {
		for gx := 0; gx < 3; gx++ {
			for dy := 0; dy < 12; dy++ {
				for dx := 0; dx < 18; dx++ {
					img.SetRGBA(foreX+12+gx*24+dx, foreY+20+gy*18+dy, color.RGBA{accent.R / 2, accent.G / 2, accent.B / 2, 220})
				}
			}
		}
	}
}

// generatePlaceholderPoster renders an elegant, content-aware poster for items
func generatePlaceholderPoster(id, name, kind string, year int) image.Image {
	const w, h = 360, 540
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	pal := pickPalette(id + ":" + name)
	fillGradient(img, pal)

	// Top Badge: Type / Kind
	badgeText := "MEDIA"
	switch strings.ToLower(kind) {
	case "movie":
		badgeText = "MOVIE"
	case "series":
		badgeText = "TV SERIES"
	case "season":
		badgeText = "SEASON"
	case "episode":
		badgeText = "EPISODE"
	}
	badgeBg := color.RGBA{pal.accent.R / 4, pal.accent.G / 4, pal.accent.B / 4, 180}
	drawBadge(img, badgeText, w/2, 45, 2, pal.accent, badgeBg)

	// Center Geometric Icon (Classic Clapperboard, TV Monitor, or Film Reel)
	centerY := h * 38 / 100
	switch strings.ToLower(kind) {
	case "movie":
		drawClapperboard(img, w/2, centerY, pal.accent)
	case "series", "season":
		drawTVIcon(img, w/2, centerY, pal.accent)
	case "episode":
		drawPlayReelIcon(img, w/2, centerY, pal.accent)
	default:
		drawClapperboard(img, w/2, centerY, pal.accent)
	}

	// 中文原片名居中展示在图标下方
	displayName := strings.TrimSpace(name)
	if displayName != "" {
		drawCenteredText(img, displayName, h*68/100, 26, pal.accent)
	}

	// 底部年份与标签
	if year > 1800 {
		yearStr := strconv.Itoa(year)
		drawCenteredText(img, yearStr, h-45, 18, pal.subText)
	} else {
		drawCenteredText(img, "PREVIEW", h-45, 16, pal.subText)
	}

	return img
}

// generateLibraryFallbackPoster creates library-specific artwork
func generateLibraryFallbackPoster(id, name, kind string) image.Image {
	const w, h = 360, 540
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	pal := pickPalette("library:" + id + ":" + kind)
	fillGradient(img, pal)

	badgeText := "COLLECTION"
	if strings.Contains(strings.ToLower(kind), "movie") {
		badgeText = "MOVIES"
	} else if strings.Contains(strings.ToLower(kind), "tv") {
		badgeText = "TV SHOWS"
	}
	badgeBg := color.RGBA{pal.accent.R / 4, pal.accent.G / 4, pal.accent.B / 4, 200}
	drawBadge(img, badgeText, w/2, 45, 2, pal.accent, badgeBg)

	drawLibraryCollageIcon(img, w/2, h*38/100, pal.accent)

	// 媒体库真实中文名居中展示（如“国产剧”、“欧美电影”、“动漫”）
	displayLibName := strings.TrimSpace(name)
	if displayLibName == "" {
		displayLibName = "媒体库"
	}
	drawCenteredText(img, displayLibName, h*66/100, 28, pal.accent)
	drawCenteredText(img, "COLLECTION", h-45, 16, pal.subText)

	return img
}

// resizeBilinear scales a decoded image into fixed target dimensions
func resizeBilinear(src image.Image, targetW, targetH int) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
	bounds := src.Bounds()
	sw := bounds.Dx()
	sh := bounds.Dy()
	if sw <= 0 || sh <= 0 {
		return dst
	}

	scaleX := float64(sw) / float64(targetW)
	scaleY := float64(sh) / float64(targetH)

	for dy := 0; dy < targetH; dy++ {
		sy := bounds.Min.Y + int(float64(dy)*scaleY)
		if sy >= bounds.Max.Y {
			sy = bounds.Max.Y - 1
		}
		for dx := 0; dx < targetW; dx++ {
			sx := bounds.Min.X + int(float64(dx)*scaleX)
			if sx >= bounds.Max.X {
				sx = bounds.Max.X - 1
			}
			dst.Set(dx, dy, src.At(sx, sy))
		}
	}
	return dst
}

var (
	collageLock  sync.Mutex
	collageCache = make(map[string]struct {
		data []byte
		exp  time.Time
	})
)

// generateLibraryCollage attempts to stitch 4 movie posters into a 2x2 collage
func (a *App) generateLibraryCollage(libID, libName, libKind string) image.Image {
	// 优先查询 Series 与 Movie 头部有海报的项目
	query := `SELECT id, path, kind, poster FROM items 
		WHERE lib=? 
		ORDER BY CASE WHEN kind IN ('Series', 'Movie') THEN 0 ELSE 1 END,
		         CASE WHEN poster <> '' THEN 0 ELSE 1 END,
		         id DESC 
		LIMIT 30`
	rows, err := a.db.Query(query, libID)
	if err != nil {
		return generateLibraryFallbackPoster(libID, libName, libKind)
	}
	defer rows.Close()

	var images []image.Image
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ID, &it.Path, &it.Kind, &it.Poster); err != nil {
			continue
		}
		path := a.imagePath(it.ID, "Primary")
		if path == "" {
			continue
		}
		var img image.Image
		if path == "cover" {
			var b []byte
			if a.db.QueryRow("SELECT data FROM covers WHERE id=?", it.ID).Scan(&b) == nil {
				img, _, _ = image.Decode(bytes.NewReader(b))
			}
		} else if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
			req, e := http.NewRequest("GET", path, nil)
			if e == nil {
				cli := posterHTTP
				if tmdbCli := a.TMDBClient(); tmdbCli != nil && tmdbCli.HTTPClient() != nil {
					cli = tmdbCli.HTTPClient()
				}
				resp, e := cli.Do(req)
				if e == nil && resp.StatusCode == 200 {
					img, _, _ = image.Decode(io.LimitReader(resp.Body, 5<<20))
					resp.Body.Close()
				}
			}
		} else {
			if f, err := os.Open(path); err == nil {
				img, _, _ = image.Decode(f)
				f.Close()
			}
		}
		if img != nil && img.Bounds().Dx() > 0 && img.Bounds().Dy() > 0 {
			images = append(images, img)
			if len(images) == 4 {
				break
			}
		}
	}

	if len(images) == 0 {
		return generateLibraryFallbackPoster(libID, libName, libKind)
	}

	// Pad to 4 images if we have at least 1 image
	for len(images) < 4 {
		images = append(images, images[len(images)-1])
	}

	const totalW, totalH = 400, 600
	const cellW, cellH = 200, 300
	collage := image.NewRGBA(image.Rect(0, 0, totalW, totalH))

	positions := [][2]int{
		{0, 0},
		{cellW, 0},
		{0, cellH},
		{cellW, cellH},
	}

	for i := 0; i < 4; i++ {
		scaled := resizeBilinear(images[i], cellW, cellH)
		px, py := positions[i][0], positions[i][1]
		draw.Draw(collage, image.Rect(px, py, px+cellW, py+cellH), scaled, image.Point{}, draw.Src)
	}

	// 2px dark divider line between quadrant posters
	dividerCol := color.RGBA{15, 17, 23, 230}
	for y := 0; y < totalH; y++ {
		collage.SetRGBA(cellW-1, y, dividerCol)
		collage.SetRGBA(cellW, y, dividerCol)
	}
	for x := 0; x < totalW; x++ {
		collage.SetRGBA(x, cellH-1, dividerCol)
		collage.SetRGBA(x, cellH, dividerCol)
	}

	// Subtle bottom gradient vignette (for visual polish)
	for y := totalH - 120; y < totalH; y++ {
		alpha := float64(y-(totalH-120)) / 120.0 * 0.75
		for x := 0; x < totalW; x++ {
			orig := collage.RGBAAt(x, y)
			r := uint8(float64(orig.R) * (1.0 - alpha))
			g := uint8(float64(orig.G) * (1.0 - alpha))
			b := uint8(float64(orig.B) * (1.0 - alpha))
			collage.SetRGBA(x, y, color.RGBA{r, g, b, 255})
		}
	}

	return collage
}

// libraryPhysicalImage probes the library root directory for physical files matching image kind
func (a *App) libraryPhysicalImage(libID, kind string) string {
	var libPath string
	if a.db.QueryRow("SELECT path FROM libraries WHERE id=?", libID).Scan(&libPath) != nil || libPath == "" {
		return ""
	}
	names := map[string][]string{
		"Primary":  {"poster", "folder", "cover"},
		"Backdrop": {"backdrop", "fanart", "background"},
		"Thumb":    {"thumb", "landscape"},
		"Logo":     {"logo", "clearlogo"},
		"Banner":   {"banner"},
	}[kind]
	for _, n := range names {
		for _, ext := range []string{".jpg", ".png", ".webp", ".jpeg"} {
			if p := safeImage(filepath.Join(libPath, n+ext)); p != "" {
				return p
			}
		}
	}
	return ""
}

// libraryPhysicalPoster probes the library root directory for physical poster files
func (a *App) libraryPhysicalPoster(libID string) string {
	return a.libraryPhysicalImage(libID, "Primary")
}

func (a *App) getCachedCollage(libID string) ([]byte, bool) {
	collageLock.Lock()
	defer collageLock.Unlock()
	item, ok := collageCache[libID]
	if ok && time.Now().Before(item.exp) {
		return item.data, true
	}
	return nil, false
}

func (a *App) setCachedCollage(libID string, data []byte) {
	collageLock.Lock()
	defer collageLock.Unlock()
	collageCache[libID] = struct {
		data []byte
		exp  time.Time
	}{
		data: data,
		exp:  time.Now().Add(1 * time.Hour),
	}
}

func (a *App) invalidateLibraryCollage(libID string) {
	collageLock.Lock()
	defer collageLock.Unlock()
	delete(collageCache, libID)
}

// generatedTag produces a deterministic ETag for dynamically generated posters
func (a *App) generatedTag(i, kind string) string {
	secret := a.cursorSecret
	if secret == "" && a.db != nil {
		a.db.QueryRow("SELECT v FROM settings WHERE k='image_secret'").Scan(&secret)
	}
	if secret == "" {
		secret = "fallback-secret"
	}
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(i + "|" + kind + "|generated:v2"))
	return hex.EncodeToString(h.Sum(nil))[:32]
}

