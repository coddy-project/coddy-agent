{{- if .Tools}}

## Available tools

{{.Tools}}
{{- end}}
{{- if .Skills}}

{{.Skills}}
{{- end}}
{{- if .PlanContext}}

{{.PlanContext}}
{{- end}}
{{- if .TodoList}}

### Current todo checklist

{{.TodoList}}
{{- end}}
{{- if .Rules}}

{{.Rules}}
{{- end}}
{{- if .Instructions}}

## Project instructions

{{.Instructions}}
{{- end}}
{{- if .Memory}}

## Session memory

{{.Memory}}
{{- end}}

## Current UTC time

{{.UTCNow}}
