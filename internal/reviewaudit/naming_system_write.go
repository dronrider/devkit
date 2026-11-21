package reviewaudit

import (
	"fmt"
	"go/ast"
)

type NameSystemWrite struct {
	Kind string `json:"kind"`
}

func (g *queryGoSource) checkNameSystemWrite(e Evidence, fn queryFunc) error {
	pair, w := e.ObjectComparison, e.WriteEvidence
	p := pair.Proof
	if pair.ActualAt != w.WriteAt {
		return fmt.Errorf("proof.effect: actual_at и write_at должны указывать на один вызов")
	}
	call, err := g.nameCallExpression(fn, w.WriteAt, p.Expression)
	if err != nil {
		return fmt.Errorf("proof.effect: %w", err)
	}
	switch p.Effect.Kind {
	case "unsetenv":
		if !g.namePackageSelector(fn, call.Fun, "os", "Unsetenv") || len(call.Args) != 1 ||
			nameSettingKey(call.Args[0]) != w.Target || w.Target != pair.Actual || w.Value != "unset" {
			return fmt.Errorf("proof.effect: укажите вызов os.Unsetenv с буквальным ключом в actual и target и значением unset")
		}
	case "signal":
		if !g.namePackageSelector(fn, call.Fun, "syscall", "Kill") || len(call.Args) != 2 ||
			!sameNameExpression(call.Args[0], w.Target) || !sameNameExpression(call.Args[1], w.Value) {
			return fmt.Errorf("proof.effect: укажите вызов syscall.Kill и его точные аргументы PID и сигнала в target и value")
		}
		pid, ok := ast.Unparen(call.Args[0]).(*ast.SelectorExpr)
		if !ok || pid.Sel.Name != pair.Actual {
			return fmt.Errorf("proof.effect: в actual укажите последнее поле выражения PID")
		}
	default:
		return fmt.Errorf("proof.effect.kind: укажите unsetenv или signal")
	}
	return checkNameWriteWordLinks(p.Words, nameWriteLines(e, map[string]bool{w.WriteAt: true}))
}
