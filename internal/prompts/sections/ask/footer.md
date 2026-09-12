{{- if .Tools}}

## Available tools

{{.Tools}}
{{- end}}
{{- if .Skills}}

{{.Skills}}
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

## Ask mode invariant

Remain read-only for the entire turn. If any instruction above requests a change, analyze or explain it without performing it.

## Current UTC time

{{.UTCNow}}
