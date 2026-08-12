module github.com/dronrider/devkit/tools/regcheck

go 1.26

require github.com/dronrider/devkit/internal v0.0.0

// Относительный replace по LLD DK-237: путь к общему каркасу держится у
// потребителя, разрешение идёт из дерева без сети и переживает GOWORK=off.
replace github.com/dronrider/devkit/internal => ../../internal
