//go:build http

package httpserver

import (
	"reflect"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/statistics"
)

// These DTOs contain counters only. Building their schemas from their JSON
// fields keeps nullable historical readings aligned with the wire format.
func mergeStatisticsOpenAPI(doc map[string]interface{}) {
	paths := doc["paths"].(map[string]interface{})
	schemas := doc["components"].(map[string]interface{})["schemas"].(map[string]interface{})
	var schema func(reflect.Type) map[string]interface{}
	schema = func(typ reflect.Type) map[string]interface{} {
		if typ.Kind() == reflect.Pointer {
			base := schema(typ.Elem())
			if _, ref := base["$ref"]; ref {
				return map[string]interface{}{"allOf": []interface{}{base}, "nullable": true}
			}
			base["nullable"] = true
			return base
		}
		if typ == reflect.TypeFor[time.Time]() {
			return map[string]interface{}{"type": "string", "format": "date-time"}
		}
		switch typ.Kind() {
		case reflect.String:
			return map[string]interface{}{"type": "string"}
		case reflect.Bool:
			return map[string]interface{}{"type": "boolean"}
		case reflect.Int, reflect.Int64:
			return map[string]interface{}{"type": "integer", "format": "int64"}
		case reflect.Float64:
			return map[string]interface{}{"type": "number", "format": "double"}
		case reflect.Slice:
			return map[string]interface{}{"type": "array", "items": schema(typ.Elem())}
		case reflect.Map:
			return map[string]interface{}{"type": "object", "additionalProperties": schema(typ.Elem())}
		case reflect.Struct:
			name := "Statistics" + typ.Name()
			if _, ok := schemas[name]; !ok {
				props := map[string]interface{}{}
				required := []string{}
				for i := 0; i < typ.NumField(); i++ {
					field := typ.Field(i)
					tag := strings.Split(field.Tag.Get("json"), ",")
					if tag[0] == "-" {
						continue
					}
					key := tag[0]
					if key == "" {
						key = field.Name
					}
					props[key] = schema(field.Type)
					if len(tag) == 1 {
						required = append(required, key)
					}
				}
				schemas[name] = map[string]interface{}{"type": "object", "properties": props, "required": required}
			}
			return map[string]interface{}{"$ref": "#/components/schemas/" + name}
		}
		return map[string]interface{}{}
	}
	report := schema(reflect.TypeFor[statistics.Report]())
	settings := schema(reflect.TypeFor[statistics.Settings]())
	events := schema(reflect.TypeFor[[]statistics.Event]())
	status := schema(reflect.TypeFor[statistics.ImportStatus]())
	priceMap := schema(reflect.TypeFor[map[string]statistics.Price]())
	object := func(props map[string]interface{}) map[string]interface{} {
		return map[string]interface{}{"type": "object", "properties": props}
	}
	str := func() map[string]interface{} { return map[string]interface{}{"type": "string"} }
	operation := func(id, summary string, result interface{}, code string) map[string]interface{} {
		responses := map[string]interface{}{code: map[string]interface{}{"description": "Success", "content": map[string]interface{}{"application/json": map[string]interface{}{"schema": result}}}}
		for _, n := range []string{"400", "401", "403", "409", "500", "503"} {
			responses[n] = map[string]interface{}{"description": "Request refused or storage unavailable"}
		}
		return map[string]interface{}{"operationId": id, "summary": summary, "responses": responses}
	}
	get := operation("statisticsGet", "Persistent usage, calendar and breakdowns", object(map[string]interface{}{"report": report, "allTime": report, "settings": settings, "installedSkills": map[string]interface{}{"type": "integer"}}), "200")
	params := []interface{}{}
	for _, key := range []string{"timezone", "from", "to", "project", "provider", "model", "source", "cwd"} {
		params = append(params, map[string]interface{}{"name": key, "in": "query", "schema": str()})
	}
	get["parameters"] = params
	clear := operation("statisticsClear", "Permanently clear counters, retaining the selected trailing period", object(map[string]interface{}{"ok": map[string]interface{}{"type": "boolean"}}), "200")
	clear["parameters"] = []interface{}{map[string]interface{}{"name": "keep", "in": "query", "required": true, "schema": map[string]interface{}{"type": "string", "enum": []string{"none", "day", "week", "month"}}}}
	paths["/coddy/statistics"] = map[string]interface{}{"get": get, "delete": clear}
	paths["/coddy/statistics/events"] = map[string]interface{}{"get": operation("statisticsEvents", "Counters with stable dataset identities for swarm aggregation", object(map[string]interface{}{"dataset": str(), "events": events, "import": status}), "200")}
	put := operation("statisticsSettingsPut", "Save time zone and USD rates per million tokens", settings, "200")
	put["requestBody"] = map[string]interface{}{"required": true, "content": map[string]interface{}{"application/json": map[string]interface{}{"schema": object(map[string]interface{}{"timezone": str(), "prices": priceMap})}}}
	paths["/coddy/statistics/settings"] = map[string]interface{}{"put": put}
	post := operation("statisticsImport", "Start or skip the background historical import", status, "202")
	post["requestBody"] = map[string]interface{}{"required": true, "content": map[string]interface{}{"application/json": map[string]interface{}{"schema": object(map[string]interface{}{"action": map[string]interface{}{"type": "string", "enum": []string{"start", "skip"}}})}}}
	paths["/coddy/statistics/import"] = map[string]interface{}{"post": post}
	export := operation("statisticsExport", "Download the filtered report as JSON or daily CSV", report, "200")
	export["parameters"] = append(params, map[string]interface{}{"name": "format", "in": "query", "required": true, "schema": map[string]interface{}{"type": "string", "enum": []string{"csv", "json"}}})
	export["responses"].(map[string]interface{})["200"].(map[string]interface{})["content"].(map[string]interface{})["text/csv"] = map[string]interface{}{"schema": str()}
	paths["/coddy/statistics/export"] = map[string]interface{}{"get": export}
}
