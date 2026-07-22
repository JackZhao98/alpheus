package main

import "testing"

func TestUserWebFetchRequestRequiresOneExplicitPublicURL(t *testing.T) {
	request, ok := userWebFetchRequest("请读一下 https://example.com/research. 然后总结")
	if !ok || request.URL != "https://example.com/research" || request.MaxChars != 12000 {
		t.Fatalf("request=%+v ok=%v", request, ok)
	}
	for _, prompt := range []string{
		"不要抓取 https://127.0.0.1/private",
		"比较 https://example.com/a 和 https://example.org/b",
		"没有链接的市场问题",
	} {
		if _, ok := userWebFetchRequest(prompt); ok {
			t.Fatalf("unexpected Tool proposal for %q", prompt)
		}
	}
}
