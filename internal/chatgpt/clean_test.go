package chatgpt

import "testing"

func TestCleanAssistantParts(t *testing.T) {
	input := "| Task | <badge color=\"success\">推荐</badge> |\n<box>\n内容\n</box>\n<T> `Task<T>`\n```xml\n<Grid><badge>x</badge></Grid>\n```\n<Link url=\"https://example.com\" title=\"文档\"/> <icon name=\"copy\"/> <button size=\"sm\" onClick={() => GenUI.copy(\"admin\")}>复制</button> <list-item>条目</list-item> <radio value=\"a\">选项</radio>"
	want := "| Task | 推荐 |\n\n内容\n\n<T> `Task<T>`\n```xml\n<Grid><badge>x</badge></Grid>\n```\n[文档](https://example.com)  复制 条目 选项"
	if got := cleanAssistantParts(input); got != want {
		t.Fatalf("cleanAssistantParts() = %q, want %q", got, want)
	}
}
