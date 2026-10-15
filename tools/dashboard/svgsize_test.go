package main

import (
	"regexp"
	"strings"
	"testing"
)

// Svg без ширины и высоты браузер рисует триста на сто пятьдесят точек. Так
// каретки сворачивания разделов колонки выросли во всю панель (DK-1388).
// Размер иконке дают две дороги: атрибут разметки либо правило стилей. У
// каждого svg статичной разметки обязана остаться одна из них. Значки шаблона
// icons копирует js, размер им даёт место вставки, их проверка не трогает.

func TestStaticSvgHaveSize(t *testing.T) {
	html := readStatic(t, "index.html")
	rules := cssRules(readStatic(t, "style.css"))
	found := 0
	for _, el := range walkSvg(html) {
		if el.dataIco {
			continue
		}
		found++
		if el.widthAttr {
			continue
		}
		if !ruleSizes(rules, el) {
			t.Errorf("svg %s без размера: ни атрибута width, ни правила стилей с шириной, браузер нарисует 300 на 150", el.label)
		}
	}
	if found < 5 {
		t.Fatalf("svg в разметке нашлось %d: разбор смотрит не туда", found)
	}
}

// svgEl это svg из статичной разметки со своим классом, цепочкой предков и
// признаками размера.
type svgEl struct {
	label     string
	own       []string
	chain     []string
	ancestor  []string
	widthAttr bool
	dataIco   bool
}

var (
	tagRe     = regexp.MustCompile(`<(/?)([a-zA-Z][a-zA-Z0-9-]*)((?:[^>"']|"[^"]*"|'[^']*')*?)(/?)>`)
	attrClass = regexp.MustCompile(`class="([^"]*)"`)
	attrID    = regexp.MustCompile(`id="([^"]*)"`)
	attrWidth = regexp.MustCompile(`(?:^|\s)width="`)
	attrIco   = regexp.MustCompile(`data-ico="`)
	widthDecl = regexp.MustCompile(`(^|[;{\s])width\s*:`)
	commentRe = regexp.MustCompile(`<!--[\s\S]*?-->`)
)

var voidTags = map[string]bool{
	"meta": true, "link": true, "input": true, "br": true, "hr": true,
	"img": true, "source": true, "area": true, "base": true, "col": true,
	"embed": true, "param": true, "track": true, "wbr": true,
}

// walkSvg проходит разметку тегами с стеком открытых элементов и отдаёт
// каждый svg с классами его предков.
func walkSvg(html string) []svgEl {
	html = commentRe.ReplaceAllString(html, "")
	type open struct {
		tag     string
		classes []string
	}
	var stack []open
	var out []svgEl
	for _, m := range tagRe.FindAllStringSubmatch(html, -1) {
		closing, tag, attrs, selfClose := m[1] == "/", strings.ToLower(m[2]), m[3], m[4] == "/"
		if closing {
			for i := len(stack) - 1; i >= 0; i-- {
				if stack[i].tag == tag {
					stack = stack[:i]
					break
				}
			}
			continue
		}
		var classes []string
		if c := attrClass.FindStringSubmatch(attrs); c != nil {
			classes = strings.Fields(c[1])
		}
		if tag == "svg" {
			chain := append([]string{}, classes...)
			var ancestors []string
			for _, o := range stack {
				chain = append(chain, o.classes...)
				ancestors = append(ancestors, o.tag)
			}
			label := "без имени"
			if id := attrID.FindStringSubmatch(attrs); id != nil {
				label = "#" + id[1]
			} else if len(classes) > 0 {
				label = "." + strings.Join(classes, ".")
			}
			out = append(out, svgEl{
				label:     label,
				own:       classes,
				chain:     chain,
				ancestor:  ancestors,
				widthAttr: attrWidth.MatchString(attrs),
				dataIco:   attrIco.MatchString(attrs),
			})
		}
		if selfClose || voidTags[tag] {
			continue
		}
		stack = append(stack, open{tag: tag, classes: classes})
	}
	return out
}

// ruleSizes решает, ставит ли хоть одно правило стилей этому svg ширину.
// Последняя часть селектора обязана указывать на сам svg: тегом svg либо его
// классом. Ранние части сверяются с классами и тегами предков.
func ruleSizes(rules [][2]string, el svgEl) bool {
	for _, rule := range rules {
		if !widthDecl.MatchString(rule[1]) {
			continue
		}
		for _, sel := range strings.Split(rule[0], ",") {
			if selectorTargets(sel, el) {
				return true
			}
		}
	}
	return false
}

var compoundRe = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9-]*)?((?:\.[a-zA-Z0-9_-]+)*)$`)

// selectorTargets решает, может ли селектор указывать на этот svg.
func selectorTargets(sel string, el svgEl) bool {
	var parts []string
	for _, f := range strings.Fields(sel) {
		if f == ">" || f == "+" || f == "~" {
			continue
		}
		parts = append(parts, f)
	}
	if len(parts) == 0 {
		return false
	}
	last := compoundOf(parts[len(parts)-1])
	if last == nil {
		return false
	}
	if last.tag != "" && last.tag != "svg" {
		return false
	}
	if last.tag == "" && len(last.classes) == 0 {
		return false
	}
	for _, c := range last.classes {
		if !contains(el.own, c) {
			return false
		}
	}
	for _, p := range parts[:len(parts)-1] {
		anc := compoundOf(p)
		if anc == nil {
			return false
		}
		if anc.tag != "" && !contains(el.ancestor, anc.tag) {
			return false
		}
		for _, c := range anc.classes {
			if !contains(el.chain, c) {
				return false
			}
		}
	}
	return true
}

type compound struct {
	tag     string
	classes []string
}

// compoundOf разбирает составную часть селектора вида button.shead: тег и
// классы, псевдоклассы и атрибуты снимаются.
func compoundOf(part string) *compound {
	if i := strings.IndexAny(part, ":["); i >= 0 {
		part = part[:i]
	}
	m := compoundRe.FindStringSubmatch(part)
	if m == nil {
		return nil
	}
	var classes []string
	for _, c := range strings.Split(m[2], ".") {
		if c != "" {
			classes = append(classes, c)
		}
	}
	return &compound{tag: m[1], classes: classes}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
