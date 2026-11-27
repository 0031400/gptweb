package chatgpt

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDeriveDILBlocksFromBody(t *testing.T) {
	block := "{@body const [x,setX] = DIL.useState(\"\")}\n<box>\n  <button>点击</button>\n</box>"
	text := "前面\n\n" + block + "\n\n后面"
	blocks := deriveDILBlocks(text)
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}
	start := utf8.RuneCountInString("前面\n\n")
	end := start + utf8.RuneCountInString(block)
	if blocks[0].Type != "dil_block" || blocks[0].StartIndex != start || blocks[0].EndIndex != end {
		t.Fatalf("block = %#v, want %d:%d", blocks[0], start, end)
	}
}

func TestDeriveDILBlocksFromInteractiveRow(t *testing.T) {
	row := `<row><button onClick={() => GenUI.issueNewTurn("x")}>按钮</button></row>`
	text := "前面\n" + row + "\n后面"
	blocks := deriveDILBlocks(text)
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}
	if got := string([]rune(text)[blocks[0].StartIndex:blocks[0].EndIndex]); got != row {
		t.Fatalf("range = %q, want %q", got, row)
	}
}

func TestMergeDILBlocksHidesNestedGenUI(t *testing.T) {
	text := "{@body const [x,setX] = DIL.useState(1)}\n<box>\n<Chart content={{}}/>\n</box>"
	blocks := deriveDILBlocks(text)
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}
	chart := GenUIComponent{Type: "charts_widget_v2", StartIndex: blocks[0].StartIndex + 10, EndIndex: blocks[0].EndIndex - 2}
	merged := mergeDILBlocks([]GenUIComponent{chart}, blocks)
	if len(merged) != 1 || !strings.EqualFold(merged[0].Type, "dil_block") {
		t.Fatalf("merged = %#v", merged)
	}
}
