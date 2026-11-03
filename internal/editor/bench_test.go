package editor

import (
	"strings"
	"testing"
)

func BenchmarkViewLargeBuffer(b *testing.B) {
	e := New()
	e.Highlight = true
	e.SetSize(120, 30)
	line := "select a.id, to_char(sysdate, 'YYYY') y, :bind from schema.table_name a where a.n > 1.5 -- c"
	e.SetText(strings.Repeat(line+"\n", 20000))
	e.HandleKey("ctrl+end", "") // view at the bottom: all lines above are lexed
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = e.View()
	}
}
