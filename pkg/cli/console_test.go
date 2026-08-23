package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/stream"
)

func TestConsolePlainModeIsAppendOnly(t *testing.T) {
	var buf bytes.Buffer
	c := NewConsole(&buf, 80, false)
	c.SetSticky([]string{"status", "slm › "}, -1)
	c.Write("first line")
	c.Write("second line")

	out := buf.String()
	if strings.Contains(out, "\033[2J") || strings.Contains(out, "\033[H") {
		t.Fatalf("non-sticky console must never clear the screen: %q", out)
	}
	if !strings.Contains(out, "first line") || !strings.Contains(out, "second line") {
		t.Fatalf("transcript lost lines: %q", out)
	}
}

func TestConsoleStickyRepaintsWithoutClearingScrollback(t *testing.T) {
	var buf bytes.Buffer
	c := NewConsole(&buf, 80, true)
	c.SetSticky([]string{"● status", "slm › "}, -1)
	buf.Reset()

	c.Write("agent said something")
	out := buf.String()
	if strings.Contains(out, "\033[2J") {
		t.Fatalf("sticky repaint must not wipe the screen: %q", out)
	}
	// It erases only from the sticky block downwards.
	if !strings.Contains(out, "\033[J") {
		t.Fatalf("expected an erase-to-end-of-screen: %q", out)
	}
	if !strings.Contains(out, "agent said something") {
		t.Fatalf("transcript line missing: %q", out)
	}
	if !strings.Contains(out, "slm › ") {
		t.Fatalf("sticky prompt not repainted: %q", out)
	}
}

func TestConsoleRawModeUsesCRLF(t *testing.T) {
	var buf bytes.Buffer
	c := NewConsole(&buf, 80, true)
	c.SetRaw(true)
	c.Write("line one\nline two")
	if !strings.Contains(buf.String(), "line one\r\nline two") {
		t.Fatalf("raw mode needs CRLF: %q", buf.String())
	}
}

func TestConsoleTruncatesStickyToWidth(t *testing.T) {
	var buf bytes.Buffer
	c := NewConsole(&buf, 20, true)
	c.SetSticky([]string{strings.Repeat("x", 100)}, -1)
	for _, line := range strings.Split(buf.String(), "\n") {
		if VisibleWidth(line) > 20 {
			t.Fatalf("sticky line exceeds width: %d %q", VisibleWidth(line), line)
		}
	}
}

func TestConsoleClearSticky(t *testing.T) {
	var buf bytes.Buffer
	c := NewConsole(&buf, 80, true)
	c.SetSticky([]string{"a", "b"}, -1)
	buf.Reset()
	c.ClearSticky()
	if !strings.Contains(buf.String(), "\033[J") {
		t.Fatalf("expected an erase: %q", buf.String())
	}
	buf.Reset()
	c.Write("after")
	if strings.Contains(buf.String(), "\033[A") {
		t.Fatalf("no cursor-up expected once the sticky block is gone: %q", buf.String())
	}
}

func TestConsoleSetWidth(t *testing.T) {
	c := NewConsole(&bytes.Buffer{}, 80, true)
	c.SetWidth(120)
	if c.Width() != 120 {
		t.Fatalf("width=%d", c.Width())
	}
	c.SetWidth(0) // ignored
	if c.Width() != 120 {
		t.Fatalf("width=%d", c.Width())
	}
}

func TestDashboardIsAppendOnlyAndFitsWidth(t *testing.T) {
	SetColorMode(ColorNever)
	var buf bytes.Buffer
	RenderDashboard(&buf, DashboardState{
		Root: "/tmp/demo", Provider: "omlx", Model: "qwen", Endpoint: "http://x/v1",
		Backend: "slmcode", Phase: "execute", Running: true,
		Events: []stream.Event{{Kind: stream.KindAgentStart, Agent: "worker", Message: "edit ●▸─⚠"}},
	})
	out := buf.String()
	if strings.Contains(out, "\033[2J") || strings.Contains(out, "\033[H") {
		t.Fatal("the dashboard must not clear the screen — that destroys scrollback")
	}
	width, _ := TermSize()
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if VisibleWidth(line) > width {
			t.Fatalf("dashboard line %d cells wide, terminal is %d: %q", VisibleWidth(line), width, line)
		}
	}
}

func TestDashboardBordersAlignWithMultibyteContent(t *testing.T) {
	SetColorMode(ColorNever)
	var buf bytes.Buffer
	RenderDashboard(&buf, DashboardState{
		Root: "/x", Provider: "omlx", Model: "m", Endpoint: "e", Backend: "b",
		Events: []stream.Event{
			{Kind: stream.KindAgentStart, Agent: "worker", Message: "●▸─⚠ multibyte"},
			{Kind: stream.KindFileChange, Agent: "worker", Message: "世界 wide runes"},
		},
	})
	var widths []int
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if strings.HasPrefix(line, "│") && strings.HasSuffix(line, "│") {
			widths = append(widths, VisibleWidth(line))
		}
	}
	if len(widths) < 3 {
		t.Fatalf("expected several boxed rows, got %d", len(widths))
	}
	for i, w := range widths {
		if w != widths[0] {
			t.Fatalf("row %d is %d cells wide, row 0 is %d — borders are misaligned", i, w, widths[0])
		}
	}
}

func TestClampWidth(t *testing.T) {
	if clampWidth(10) != 40 {
		t.Fatal("narrow terminals clamp up to 40")
	}
	if clampWidth(300) != 120 {
		t.Fatal("wide terminals clamp down to 120")
	}
	if clampWidth(90) != 90 {
		t.Fatal("normal widths pass through")
	}
}

func TestNarrowLayout(t *testing.T) {
	if !NarrowLayout(60) || NarrowLayout(100) {
		t.Fatal("narrow layout threshold is 70")
	}
}
