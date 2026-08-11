module github.com/dronrider/devkit/tools/cmdout

go 1.26

require github.com/dronrider/devkit/internal v0.0.0

// Относительный replace по LLD DK-237: модуль-потребитель держит путь к общему
// каркасу у себя, разрешение идёт из дерева без сети и переживает GOWORK=off,
// тарболл релиза и голый go build мимо devkit.
replace github.com/dronrider/devkit/internal => ../../internal
