package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// extractJSFunc достаёт тело функции из исходника app.js по имени, включая
// сигнатуру: от строки «function <имя>(» до закрывающей фигурной скобки её
// тела, счётом вложенности. Функции кольца, которые сюда идут, не держат
// строк и регулярок с фигурными скобками внутри, простой счёт для них точен.
func extractJSFunc(t *testing.T, src, name string) string {
	t.Helper()
	marker := "function " + name + "("
	at := strings.Index(src, marker)
	if at < 0 {
		t.Fatalf("функция %s не найдена в app.js", name)
	}
	open := strings.Index(src[at:], "{")
	if open < 0 {
		t.Fatalf("у функции %s нет тела", name)
	}
	start := at + open
	depth := 0
	for i := start; i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[at:i+1] + "\n"
			}
		}
	}
	t.Fatalf("у функции %s не нашлась закрывающая скобка", name)
	return ""
}

// extractJSConst достаёт объявление константы одной строкой, от «const
// <имя> =» до конца строки.
func extractJSConst(t *testing.T, src, name string) string {
	t.Helper()
	marker := "const " + name + " ="
	at := strings.Index(src, marker)
	if at < 0 {
		t.Fatalf("константа %s не найдена в app.js", name)
	}
	end := strings.Index(src[at:], "\n")
	if end < 0 {
		t.Fatalf("у константы %s не нашёлся конец строки", name)
	}
	return src[at:at+end] + "\n"
}

// ringMiddleCase гоняет ringMiddle(p, list) в node с подложным document,
// достаточным для svgEl/svgAttrs, и отдаёт tag плюс class верхнего узла
// результата ("" при null).
func ringMiddleCase(t *testing.T, node, harness, pJSON, listJSON string) (tag, class string) {
	t.Helper()
	script := harness + `
const p = ` + pJSON + `;
const list = ` + listJSON + `;
const out = ringMiddle(p, list);
process.stdout.write(JSON.stringify(out ? {tag: out.tag, class: out.attrs.class || ""} : {tag: "", class: ""}));
`
	dir := t.TempDir()
	path := filepath.Join(dir, "case.js")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, path).CombinedOutput()
	if err != nil {
		t.Fatalf("прогон node: %v\n%s", err, out)
	}
	var got struct{ Tag, Class string }
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("разбор ответа node %q: %v", out, err)
	}
	return got.Tag, got.Class
}

// Ракета в середине кольца это DK-978: живая работающая сессия без своего
// плана получает значок вместо цифры, а не наоборот. Тест гоняет саму
// функцию app.js в node, а не переписанную копию логики: иначе правка одного
// файла красила бы только зеркало, а не источник.
func TestRingMiddlePicksRocketOnlyForWorkingSessionWithoutPlan(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден, поведение кольца не гоняется")
	}
	app, err := os.ReadFile(filepath.Join("static", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(app)
	var b strings.Builder
	b.WriteString(`
function svgEl(tag, cls) {
  const node = { tag, attrs: {}, children: [] };
  node.setAttribute = (k, v) => { node.attrs[k] = String(v); };
  node.append = (...kids) => { node.children.push(...kids); };
  if (cls) node.setAttribute("class", cls);
  return node;
}
`)
	b.WriteString(extractJSConst(t, src, "RING_NS"))
	b.WriteString(extractJSFunc(t, src, "svgAttrs"))
	b.WriteString(extractJSFunc(t, src, "ringNum"))
	b.WriteString(extractJSFunc(t, src, "ringNumber"))
	b.WriteString(extractJSFunc(t, src, "ringRocket"))
	b.WriteString(extractJSFunc(t, src, "ringMiddle"))
	harness := b.String()

	cases := []struct {
		name      string
		p, list   string
		wantTag   string
		wantClass string
	}{
		{
			name: "живая работа без плана это ракета",
			p:    `{"state":"working","working":1,"parked":false}`, list: "[]",
			wantTag: "g", wantClass: "rrocket",
		},
		{
			name: "план есть, значит дробь, а не ракета, даже в работе",
			p:    `{"state":"working","working":1,"parked":false}`,
			list: `[{"state":"completed"},{"state":"pending"}]`,
			wantTag: "g", wantClass: "rfrac",
		},
		{
			name: "ждёт человека это число ждущих, не ракета",
			p:    `{"state":"waiting","waiting":2,"parked":false}`, list: "[]",
			wantTag: "text", wantClass: "rnum",
		},
		{
			name: "запаркованная строка ракеты не получает",
			p:    `{"state":"working","working":1,"parked":true}`, list: "[]",
			wantTag: "", wantClass: "",
		},
		{
			name: "рабочих нет, ракете не из чего взяться",
			p:    `{"state":"working","working":0,"parked":false}`, list: "[]",
			wantTag: "", wantClass: "",
		},
		{
			name: "разговоров нет вовсе, кольцо пустое",
			p:    "null", list: "[]",
			wantTag: "", wantClass: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tag, class := ringMiddleCase(t, node, harness, c.p, c.list)
			if tag != c.wantTag || class != c.wantClass {
				t.Fatalf("ringMiddle(%s, %s) = {%s %s}, жду {%s %s}",
					c.p, c.list, tag, class, c.wantTag, c.wantClass)
			}
		})
	}
}
