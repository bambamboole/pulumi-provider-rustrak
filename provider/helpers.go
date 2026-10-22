package provider

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"

	"github.com/bambamboole/pulumi-provider-rustrak/internal/rustrak"
)

// clientKey carries a client injected directly into the context; tests use it
// to exercise resource methods without a configured provider.
type clientKey struct{}

// client returns the Rustrak API client configured by the provider.
func client(ctx context.Context) *rustrak.Client {
	if c, ok := ctx.Value(clientKey{}).(*rustrak.Client); ok {
		return c
	}
	return infer.GetConfig[Config](ctx).client
}

// diffArgs compares the pulumi-tagged fields of old and new argument structs
// and returns a property diff per changed field. Fields listed in replaces
// trigger a replacement instead of an update. Nil and empty slices or maps are
// considered equal, so an omitted list never diffs against an empty one.
func diffArgs[A any](old, new A, replaces ...string) map[string]p.PropertyDiff {
	diff := map[string]p.PropertyDiff{}
	oldValue, newValue := reflect.ValueOf(old), reflect.ValueOf(new)
	typ := oldValue.Type()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		tag := field.Tag.Get("pulumi")
		if tag == "" || !field.IsExported() {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if equalValues(oldValue.Field(i), newValue.Field(i)) {
			continue
		}
		kind := p.Update
		for _, replace := range replaces {
			if replace == name {
				kind = p.UpdateReplace
			}
		}
		diff[name] = p.PropertyDiff{Kind: kind, InputDiff: true}
	}
	return diff
}

func equalValues(a, b reflect.Value) bool {
	switch a.Kind() {
	case reflect.Slice, reflect.Map:
		if a.Len() == 0 && b.Len() == 0 {
			return true
		}
	case reflect.Pointer:
		if a.IsNil() || b.IsNil() {
			return a.IsNil() == b.IsNil()
		}
		return equalValues(a.Elem(), b.Elem())
	}
	return reflect.DeepEqual(a.Interface(), b.Interface())
}

// diffResponse builds the infer response for a detailed diff.
func diffResponse(diff map[string]p.PropertyDiff) infer.DiffResponse {
	return infer.DiffResponse{HasChanges: len(diff) > 0, DetailedDiff: diff}
}

// parseID parses a numeric resource ID.
func parseID(id, format string) (int64, error) {
	value, err := strconv.ParseInt(id, 10, 64)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("rustrak: resource ID %q must have the form %s", id, format)
	}
	return value, nil
}

// splitProjectID splits an ID of the form <projectId>/<rest>.
func splitProjectID(id, format string) (int64, string, error) {
	projectPart, rest, ok := strings.Cut(id, "/")
	if !ok || rest == "" || strings.Contains(rest, "/") {
		return 0, "", fmt.Errorf("rustrak: resource ID %q must have the form %s", id, format)
	}
	projectID, err := parseID(projectPart, format)
	if err != nil {
		return 0, "", fmt.Errorf("rustrak: resource ID %q must have the form %s", id, format)
	}
	return projectID, rest, nil
}

// deref returns the string behind an optional API field.
func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// optional returns nil for an empty string so the API leaves the field out.
func optional(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// optionalInt returns nil for zero, which the resources use for "unset".
func optionalInt(value int) *int64 {
	if value == 0 {
		return nil
	}
	converted := int64(value)
	return &converted
}

// intValue returns zero for an unset API number.
func intValue(value *int64) int {
	if value == nil {
		return 0
	}
	return int(*value)
}
