package embyserver

import (
	"image"
	"image/color"
	"testing"
)

func TestVectorFontRendering(t *testing.T) {
	// 1. 验证矢量字体是否正常加载
	font := getVectorFont()
	if font == nil {
		t.Fatal("内置矢量中文字体加载失败: font is nil")
	}

	// 2. 验证中文文本测量宽度
	text := "国产剧 - 繁花"
	w := measureVectorText(text, 24)
	if w <= 0 {
		t.Fatalf("文本测量宽度异常: text=%s, width=%d", text, w)
	}

	// 3. 验证生成单片带中文的兜底海报
	img := generatePlaceholderPoster("test-item", "流浪地球2", "Movie", 2023)
	if img == nil || img.Bounds().Dx() != 360 || img.Bounds().Dy() != 540 {
		t.Fatalf("生成的单片海报尺寸异常")
	}

	// 4. 验证生成媒体库带中文的兜底海报
	libImg := generateLibraryFallbackPoster("lib-test", "国产剧", "tvshows")
	if libImg == nil || libImg.Bounds().Dx() != 360 || libImg.Bounds().Dy() != 540 {
		t.Fatalf("生成的媒体库海报尺寸异常")
	}

	// 5. 验证 drawCenteredText 在 RGBA 画布上绘制中文
	canvas := image.NewRGBA(image.Rect(0, 0, 300, 100))
	drawCenteredText(canvas, "中文测试", 50, 20, color.RGBA{255, 255, 255, 255})
}
