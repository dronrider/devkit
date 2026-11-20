package reviewaudit

import "testing"

func TestNameSettingFlatEnvironment(t *testing.T) {
	source, at := nameProofFixture(t, `package sample
import "testing"
import "os"
func TestOwn(t *testing.T) {
 t.Setenv("TOKEN", "value")
 if got := Own(); got != "value" { t.Fatal(got) }
}

func Own() string {
 return os.Getenv("TOKEN")
}
`)
	g := newQueryGoSource(source)
	fn, err := g.declaration(at("func TestOwn"))
	if err != nil {
		t.Fatal(err)
	}
	p := &NameObjectProof{Expression: `t.Setenv("TOKEN", "value")`, Setting: &NameSettingWrite{SubtestAt: at("func TestOwn"), Use: NameCodeUse{Declaration: at("func Own"), At: at("if got"), Expression: "Own()"}, ReadAt: at("return os.Getenv")}}
	e := Evidence{Result: "checked", ObjectComparison: &NameObjectComparison{Actual: "TOKEN", ActualAt: at("t.Setenv"), Proof: p}, WriteEvidence: &WriteEvidence{Target: "TOKEN", Value: `"value"`, WriteAt: at("t.Setenv")}}
	if err := g.checkNameSettingWrite(e, fn); err != nil {
		t.Fatal(err)
	}
	e.WriteEvidence.Target = "OTHER"
	if err := g.checkNameSettingWrite(e, fn); err == nil {
		t.Fatal("подменённый ключ принят")
	}
}

func TestNameSettingRejectsEnvironmentChangedBeforeRead(t *testing.T) {
	source, at := nameProofFixture(t, `package sample
import "testing"
import "os"
func TestOwn(t *testing.T) {
 t.Setenv("TOKEN", "value")
 os.Unsetenv("TOKEN")
 if got := Own(); got != "value" { t.Fatal(got) }
}
func Own() string {
 return os.Getenv("TOKEN")
}
`)
	g := newQueryGoSource(source)
	fn, _ := g.declaration(at("func TestOwn"))
	p := &NameObjectProof{Expression: `t.Setenv("TOKEN", "value")`, Setting: &NameSettingWrite{SubtestAt: at("func TestOwn"), Use: NameCodeUse{Declaration: at("func Own"), At: at("if got"), Expression: "Own()"}, ReadAt: at("return os.Getenv")}}
	e := Evidence{Result: "checked", ObjectComparison: &NameObjectComparison{Actual: "TOKEN", ActualAt: at("t.Setenv"), Proof: p}, WriteEvidence: &WriteEvidence{Target: "TOKEN", Value: `"value"`, WriteAt: at("t.Setenv")}}
	if err := g.checkNameSettingWrite(e, fn); err == nil {
		t.Fatal("чтение изменённого ключа принято")
	}
}

// Если валидатор принимает недостижимый Getenv, тест должен покраснеть.
func TestNameSettingRejectsUnusedEnvironmentRead(t *testing.T) {
	source, at := nameProofFixture(t, `package sample
import "testing"
import "os"
func TestOwn(t *testing.T) {
 t.Setenv("TOKEN", "value")
 if got := Own(); got != "value" { t.Fatal(got) }
}
func Own() string {
 if false { _ = os.Getenv("TOKEN") }
 return "other"
}
`)
	g := newQueryGoSource(source)
	fn, _ := g.declaration(at("func TestOwn"))
	p := &NameObjectProof{Expression: `t.Setenv("TOKEN", "value")`, Setting: &NameSettingWrite{SubtestAt: at("func TestOwn"), Use: NameCodeUse{Declaration: at("func Own"), At: at("if got"), Expression: "Own()"}, ReadAt: at("if false")}}
	e := Evidence{Result: "checked", ObjectComparison: &NameObjectComparison{Actual: "TOKEN", ActualAt: at("t.Setenv"), Proof: p}, WriteEvidence: &WriteEvidence{Target: "TOKEN", Value: `"value"`, WriteAt: at("t.Setenv")}}
	if err := g.checkNameSettingWrite(e, fn); err == nil {
		t.Fatal("недостижимое чтение принято")
	}
}

// Если вызов проверки только присутствует в недостижимом блоке, тест краснеет.
func TestNameSettingRejectsUnreachableConsumer(t *testing.T) {
	source, at := nameProofFixture(t, `package sample
import "testing"
import "os"
func TestOwn(t *testing.T) {
 t.Setenv("TOKEN", "value")
 if false { if got := Own(); got != "value" { t.Fatal(got) } }
}
func Own() string { return os.Getenv("TOKEN") }
`)
	g := newQueryGoSource(source)
	fn, _ := g.declaration(at("func TestOwn"))
	p := &NameObjectProof{Expression: `t.Setenv("TOKEN", "value")`, Setting: &NameSettingWrite{SubtestAt: at("func TestOwn"), Use: NameCodeUse{Declaration: at("func Own"), At: at("if false"), Expression: "Own()"}, ReadAt: at("func Own() string")}}
	e := Evidence{Result: "checked", ObjectComparison: &NameObjectComparison{Actual: "TOKEN", ActualAt: at("t.Setenv"), Proof: p}, WriteEvidence: &WriteEvidence{Target: "TOKEN", Value: `"value"`, WriteAt: at("t.Setenv")}}
	if err := g.checkNameSettingWrite(e, fn); err == nil {
		t.Fatal("недостижимый потребитель принят")
	}
}

// Если чтение результата спрятано за false &&, оно не подтверждает проверку.
func TestNameSettingRejectsShortCircuitedConsumer(t *testing.T) {
	source, at := nameProofFixture(t, `package sample
import "testing"
import "os"
func TestOwn(t *testing.T) {
 t.Setenv("TOKEN", "value")
 if got := Own(); false && got != "value" { t.Fatal(got) }
}
func Own() string { return os.Getenv("TOKEN") }
`)
	g := newQueryGoSource(source)
	fn, _ := g.declaration(at("func TestOwn"))
	p := &NameObjectProof{Expression: `t.Setenv("TOKEN", "value")`, Setting: &NameSettingWrite{SubtestAt: at("func TestOwn"), Use: NameCodeUse{Declaration: at("func Own"), At: at("if got"), Expression: "Own()"}, ReadAt: at("func Own() string")}}
	e := Evidence{Result: "checked", ObjectComparison: &NameObjectComparison{Actual: "TOKEN", ActualAt: at("t.Setenv"), Proof: p}, WriteEvidence: &WriteEvidence{Target: "TOKEN", Value: `"value"`, WriteAt: at("t.Setenv")}}
	if err := g.checkNameSettingWrite(e, fn); err == nil {
		t.Fatal("недостижимая проверка принята")
	}
}

// Без доказанного пустого значения ранняя ветка может скрыть чтение TOKEN.
func TestNameSettingEnvironmentGuard(t *testing.T) {
	cases := []struct {
		name, setup, guard string
		want               bool
	}{
		{"unreachable", `t.Setenv("OTHER", "")`, `if true { return "other" }`, false},
		{"false", `t.Setenv("OTHER", "")`, `if false { return "other" }`, true},
		{"empty", `t.Setenv("OTHER", "")`, `if sid := strings.TrimSpace(os.Getenv("OTHER")); sid != "" { return sid }`, true},
		{"nonempty", `t.Setenv("OTHER", "value")`, `if sid := strings.TrimSpace(os.Getenv("OTHER")); sid != "" { return sid }`, false},
		{"reset", `t.Setenv("OTHER", ""); t.Setenv("OTHER", "value")`, `if sid := strings.TrimSpace(os.Getenv("OTHER")); sid != "" { return sid }`, false},
		{"else", `t.Setenv("OTHER", "")`, `if sid := strings.TrimSpace(os.Getenv("OTHER")); sid != "" { return sid } else { return "wrong" }`, false},
		{"indirect_reset", `t.Setenv("OTHER", ""); resetOther()`, `if sid := strings.TrimSpace(os.Getenv("OTHER")); sid != "" { return sid }`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source, at := nameProofFixture(t, `package sample
import "testing"
import "os"
import "strings"
func TestOwn(t *testing.T) {
 `+tc.setup+`
 t.Setenv("TOKEN", "value")
 if got := Own(); got != "value" { t.Fatal(got) }
}
func Own() string {
 `+tc.guard+`
 return os.Getenv("TOKEN")
}
func resetOther() { _ = os.Setenv("OTHER", "value") }
`)
			g := newQueryGoSource(source)
			fn, _ := g.declaration(at("func TestOwn"))
			p := &NameObjectProof{Expression: `t.Setenv("TOKEN", "value")`, Setting: &NameSettingWrite{SubtestAt: at("func TestOwn"), Use: NameCodeUse{Declaration: at("func Own"), At: at("if got"), Expression: "Own()"}, ReadAt: at("return os.Getenv")}}
			e := Evidence{Result: "checked", ObjectComparison: &NameObjectComparison{Actual: "TOKEN", ActualAt: at(`t.Setenv("TOKEN"`), Proof: p}, WriteEvidence: &WriteEvidence{Target: "TOKEN", Value: `"value"`, WriteAt: at(`t.Setenv("TOKEN"`)}}
			if err := g.checkNameSettingWrite(e, fn); (err == nil) != tc.want {
				t.Fatalf("want %v: %v", tc.want, err)
			}
		})
	}
}
