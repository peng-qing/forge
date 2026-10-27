package main

import (
	_ "embed"
	"text/template"
)

//go:embed command.tpl
var commandTemplateSource string

var generatorTemplates = template.Must(
	template.New("commandgen").Parse(commandTemplateSource),
)
