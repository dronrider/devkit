package reviewaudit

import (
	"strings"
	"testing"
)

func TestNameRepairSetting(t *testing.T) {
	for _, tc := range []struct{ key, binding, detail string }{
		{"section", "cfg := &Config{}", ""},
		{"other", "cfg := &Config{}", "proof.setting.read_at"},
		{"section", "cfg := &Other{}", "proof.setting.use.declaration"},
	} {
		t.Run(tc.key+tc.binding, func(t *testing.T) {
			source, at := nameProofFixture(t, `package sample
import "testing"
import "github.com/spf13/viper"
type Config struct{}
type Other struct{}
func TestReadsConfig(t *testing.T) {
 t.Run("case", func(t *testing.T) {
  viper.Set("section", 7)
  `+tc.binding+`
  cfg.check()
 })
}
func (c *Config) check() {
 _ = viper.GetStringMap("`+tc.key+`")
}
`)
			e := namingWire(t, `{"check":"writes","result":"checked","object_comparison":{"expected":["Test","Reads","Config"],"actual":"section","actual_at":"@viper.Set","proof":{"expression":"viper.Set(\"section\", 7)","setting":{"subtest_at":"@t.Run","use":{"at":"@cfg.check","declaration":"@func (c *Config)","expression":"cfg.check()"},"read_at":"@viper.GetStringMap"},"words":[
{"word":"Test","role":"context","fact":{"location":"@viper.Set","quote":"viper.Set(\"section\", 7)","value":"section","text":"Вход сценария."}},
{"word":"Reads","role":"context","fact":{"location":"@viper.Set","quote":"viper.Set(\"section\", 7)","value":"section","text":"Подготовка чтения."}},
{"word":"Config","role":"context","fact":{"location":"@viper.Set","quote":"viper.Set(\"section\", 7)","value":"section","text":"Ключ конфигурации."}}]}},
"write_evidence":{"word":"Config","target":"section","value":"7","write_at":"@viper.Set"}}`, at)
			assertTargetCheck(t, checkNameEntry(e, at("func TestReadsConfig"), identifierWords("TestReadsConfig"), source), tc.detail)
			e.Result = "not_applicable"
			e.Location = at("func TestReadsConfig")
			assertTargetCheck(t, checkNameEntry(e, e.Location, identifierWords("TestReadsConfig"), source), "not_applicable запрещён")
		})
	}
}

func TestNameRepairSQLArgument(t *testing.T) {
	source, at := nameProofFixture(t, `package sample
func updateRecords(db DB, id int) {
 db.Exec(
  "UPDATE records SET flag = 1 WHERE id = $1",
  id,
 )
 consume(id)
}
`)
	e := namingWire(t, `{"check":"writes","result":"checked","object_comparison":{"expected":["Records"],"actual":"records","actual_at":"@UPDATE records","proof":{"expression":"db.Exec(\"UPDATE records SET flag = 1 WHERE id = $1\", id)","sql":{"table":"records","statement":"UPDATE records SET flag = 1 WHERE id = $1","arguments":["id"]},"words":[{"word":"Records","role":"object","fact":{"location":"@id,","quote":"id,","value":"id","text":"Аргумент отбора записи."}}]}},
"write_evidence":{"word":"Records","target":"records","value":"1","write_at":"@db.Exec"}}`, at)
	assertTargetCheck(t, checkNameEntry(e, at("func updateRecords"), identifierWords("updateRecords"), source), "")
	e.ObjectComparison.Proof.Words[0].Fact.Location = at("consume(id)")
	e.ObjectComparison.Proof.Words[0].Fact.Quote = "consume(id)"
	assertTargetCheck(t, checkNameEntry(e, at("func updateRecords"), identifierWords("updateRecords"), source), "точное выражение аргумента")
}

func TestNameRepairFileContent(t *testing.T) {
	for _, tc := range []struct {
		payload, write string
		want           string
	}{
		{"payload", "os.WriteFile(path, encoded, 0600)", ""},
		{"", "os.WriteFile(path, encoded, 0600)", "write_evidence.file.payload_at"},
		{"payload", "encoded = other\n os.WriteFile(path, encoded, 0600)", "проследите значение"},
	} {
		t.Run(tc.payload+tc.write, func(t *testing.T) {
			source, at := nameProofFixture(t, `package sample
import "os"
import "encoding/pem"
import "crypto/x509"
func writeRSAKey(path string, key *rsa.PrivateKey) {
 encoded := pem.EncodeToMemory(&pem.Block{Bytes: x509.MarshalPKCS1PrivateKey(key)})
 `+tc.write+`
}
`)
			e := namingWire(t, `{"check":"writes","result":"checked","object_comparison":{"expected":["RSA","Key"],"actual":"key","actual_at":"@encoded :=","proof":{"expression":"key","words":[{"word":"RSA","role":"format","fact":{"location":"@encoded :=","quote":"x509.MarshalPKCS1PrivateKey(key)","value":"key","text":"RSA в PKCS1."}},{"word":"Key","role":"object","fact":{"location":"@encoded :=","quote":"x509.MarshalPKCS1PrivateKey(key)","value":"key","text":"Записывается ключ."}}]}},"write_evidence":{"word":"Key","target":"path","value":"encoded","write_at":"@os.WriteFile","file":{"role":"content","path":"path","payload_at":"@encoded :=","encode_at":"@encoded :="}}}`, at)
			if strings.TrimSpace(tc.payload) == "" {
				e.WriteEvidence.File.PayloadAt = ""
			}
			assertTargetCheck(t, checkNameEntry(e, at("func writeRSAKey"), identifierWords("writeRSAKey"), source), tc.want)
		})
	}
}
