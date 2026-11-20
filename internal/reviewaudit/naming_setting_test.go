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
