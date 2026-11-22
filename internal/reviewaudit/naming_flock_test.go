package reviewaudit

import (
	"strings"
	"testing"
)

func TestNameSystemFlock(t *testing.T) {
	const scalarOpen = `fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR, 0600)`
	const wrappedOpen = `fd, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)`
	const sink = `syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)`
	const wrappedSink = `syscall.Flock(int(fd.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)`
	for _, tc := range []struct {
		name, opening, beforeOpen, beforeFlock, flock, target, path, want string
	}{
		{"integer", scalarOpen, "", "", sink, "fd", "path", ""},
		{"file_object", wrappedOpen, "", "", wrappedSink, "int(fd.Fd())", "path", ""},
		{"one_file_wrapper", scalarOpen, "", "file := os.NewFile(uintptr(fd), path)", sink, "fd", "path", ""},
		{"reversed_comparison", scalarOpen, "", "", sink, "fd", "path", ""},
		{"only_on_open_error", scalarOpen, "", "", sink, "fd", "path", "при ошибке открытия"},
		{"open_error_ignored", scalarOpen, "", "", sink, "fd", "path", "при ошибке открытия"},
		{"open_error_overwritten", scalarOpen, "", "", sink, "fd", "path", "при ошибке открытия"},
		{"other_path", scalarOpen, "", "", sink, "fd", "other", "фактический путь"},
		{"different_fd", scalarOpen, "", "", strings.Replace(sink, "fd,", "other,", 1), "other", "path", "open_at"},
		{"read_only", strings.Replace(scalarOpen, "O_RDWR", "O_RDONLY", 1), "", "", sink, "fd", "path", "путь должен открываться"},
		{"descriptor_rebound", scalarOpen, "", "fd = other", sink, "fd", "path", "путь должен открываться"},
		{"descriptor_escaped", scalarOpen, "", "leak(fd)", sink, "fd", "path", "путь должен открываться"},
		{"descriptor_closed", scalarOpen, "", "file := os.NewFile(uintptr(fd), path); file.Close()", sink, "fd", "path", "путь должен открываться"},
		{"file_closed", wrappedOpen, "", "fd.Close()", wrappedSink, "int(fd.Fd())", "path", "путь должен открываться"},
		{"unlock", scalarOpen, "", "", strings.Replace(sink, "LOCK_EX", "LOCK_UN", 1), "fd", "path", "точными аргументами"},
		{"unreachable_open", scalarOpen, "return", "", sink, "fd", "path", "нужен достижимый"},
		{"unreachable_flock", scalarOpen, "", "return", sink, "fd", "path", "нужен достижимый"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actual, after, guard := "fd", "", "if err != nil { return }"
			switch tc.name {
			case "different_fd":
				actual, after = "other", "_ = fd;"
			case "one_file_wrapper":
				after = "defer file.Close();"
			case "reversed_comparison":
				guard = "if nil != err { return }"
			case "only_on_open_error":
				guard = "if err == nil { return }"
			case "open_error_ignored":
				guard = "_ = err"
			case "open_error_overwritten":
				guard = "err = nil; if err != nil { return }"
			}
			code := "package sample\nimport \"os\"\nimport \"syscall\"\nvar _ = os.O_CREATE\nfunc leak(int) {}\n" +
				"func launchLock(path string, other int) {\n" + tc.beforeOpen + "\n" +
				tc.opening + "\n" + guard + "\n" + tc.beforeFlock +
				"\nfor {\nif err := " + tc.flock + "; err == nil { " + after + " return }\n}\n}\n"
			source, at := nameCurrentProofFixture(t, code)
			const flags = "syscall.LOCK_EX|syscall.LOCK_NB"
			e := namingWire(t, `{"check":"writes","result":"checked","object_comparison":{
				"expected":["launch","Lock"],"actual":`+strconvQuote(actual)+`,"actual_at":`+strconvQuote("@"+tc.flock)+`,
				"proof":{"expression":`+strconvQuote(tc.flock)+`,"effect":{
					"kind":"flock","open_at":`+strconvQuote("@"+tc.opening)+`,"path":`+strconvQuote(tc.path)+`},
				"words":[{"word":"launch","role":"context","fact":{"location":`+strconvQuote("@"+tc.opening)+`,
					"quote":`+strconvQuote(tc.opening)+`,"value":"path","text":"Открывается файл для проверки."}},
				{"word":"Lock","role":"object","fact":{"location":`+strconvQuote("@"+tc.flock)+`,
					"quote":`+strconvQuote(tc.flock)+`,"value":"syscall.Flock","text":"Блокировка берётся на том же дескрипторе."}}]}},
				"write_evidence":{"word":"Lock","target":`+strconvQuote(tc.target)+`,"value":`+strconvQuote(flags)+`,"write_at":`+strconvQuote("@"+tc.flock)+`}}`, at)
			assertTargetCheck(t, checkNameEntry(e, at("func launchLock"), identifierWords("launchLock"), source), tc.want)
		})
	}
}
