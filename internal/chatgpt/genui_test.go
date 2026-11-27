package chatgpt

import "testing"

func TestNormalizeGenUIRawOffsetsAndData(t *testing.T) {
	tag := `<Chart content={{"chartType":"bar","data":[{"month":"1月","sales":120}]}}/>`
	text := "😀开头" + tag + "结尾"
	start := len([]rune("😀开头"))
	end := start + len([]rune(tag))
	metadata := map[string]any{
		"genui_components": []any{
			map[string]any{
				"type":        "charts_widget_v2",
				"start_index": float64(start),
				"end_index":   float64(end),
			},
		},
	}
	components := normalizeGenUI(text, metadata)
	if len(components) != 1 {
		t.Fatalf("got %d components, want 1", len(components))
	}
	if components[0].StartIndex != start || components[0].EndIndex != end {
		t.Fatalf("raw range = %d:%d, want %d:%d", components[0].StartIndex, components[0].EndIndex, start, end)
	}
	if components[0].Data["chartType"] != "bar" {
		t.Fatalf("chartType = %v", components[0].Data["chartType"])
	}
	if data, ok := components[0].Data["data"].([]any); !ok || len(data) != 1 {
		t.Fatalf("chart data = %#v", components[0].Data["data"])
	}
}

func TestNormalizeGenUIWritingBlockData(t *testing.T) {
	tag := `<WritingBlock variant="email" subject="进度">正文</WritingBlock>`
	metadata := map[string]any{
		"genui_components": []any{
			map[string]any{
				"type":        "writing_block",
				"start_index": float64(0),
				"end_index":   float64(len([]rune(tag))),
			},
		},
	}
	components := normalizeGenUI(tag, metadata)
	if len(components) != 1 {
		t.Fatalf("got %d components, want 1", len(components))
	}
	if components[0].Data["subject"] != "进度" || components[0].Data["body"] != "正文" {
		t.Fatalf("writing data = %#v", components[0].Data)
	}
}
