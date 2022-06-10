package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"golang.org/x/xerrors"
)

// QueryParamParser is a helper for parsing all query params and gathering all
// errors in 1 sweep. This means all invalid fields are returned at once,
// rather than only returning the first error
type QueryParamParser struct {
	errors []Error
}

func NewQueryParamParser() *QueryParamParser {
	return &QueryParamParser{
		errors: []Error{},
	}
}

// ValidationErrors is the set of errors to return via the API. If the length
// of this set is 0, there was no errors.
func (p QueryParamParser) ValidationErrors() []Error {
	return p.errors
}

func (p *QueryParamParser) ParseInteger(r *http.Request, def int, queryParam string) int {
	v, err := parse(r, strconv.Atoi, def, queryParam)
	if err != nil {
		p.errors = append(p.errors, Error{
			Field:  queryParam,
			Detail: fmt.Sprintf("Query param %q must be a valid integer (%s)", queryParam, err.Error()),
		})
	}
	return v
}

func (p *QueryParamParser) ParseUUIDorMe(r *http.Request, def uuid.UUID, me uuid.UUID, queryParam string) uuid.UUID {
	if r.URL.Query().Get(queryParam) == "me" {
		return me
	}
	return p.ParseUUID(r, def, queryParam)
}

func (p *QueryParamParser) ParseUUID(r *http.Request, def uuid.UUID, queryParam string) uuid.UUID {
	v, err := parse(r, uuid.Parse, def, queryParam)
	if err != nil {
		p.errors = append(p.errors, Error{
			Field:  queryParam,
			Detail: fmt.Sprintf("Query param %q must be a valid uuid", queryParam),
		})
	}
	return v
}

func (p *QueryParamParser) ParseUUIDArray(r *http.Request, def []uuid.UUID, queryParam string) []uuid.UUID {
	v, err := parse(r, func(v string) ([]uuid.UUID, error) {
		var badValues []string
		strs := strings.Split(v, ",")
		ids := make([]uuid.UUID, 0, len(strs))
		for _, s := range strs {
			id, err := uuid.Parse(strings.TrimSpace(s))
			if err != nil {
				badValues = append(badValues, v)
				continue
			}
			ids = append(ids, id)
		}

		if len(badValues) > 0 {
			return []uuid.UUID{}, xerrors.Errorf("%s", strings.Join(badValues, ","))
		}
		return ids, nil
	}, def, queryParam)
	if err != nil {
		p.errors = append(p.errors, Error{
			Field:  queryParam,
			Detail: fmt.Sprintf("Query param %q has invalid uuids: %q", queryParam, err.Error()),
		})
	}
	return v
}

func (p *QueryParamParser) ParseString(r *http.Request, def string, queryParam string) string {
	v, err := parse(r, func(v string) (string, error) {
		return v, nil
	}, def, queryParam)
	if err != nil {
		p.errors = append(p.errors, Error{
			Field:  queryParam,
			Detail: fmt.Sprintf("Query param %q must be a valid string", queryParam),
		})
	}
	return v
}

func parse[T any](r *http.Request, parse func(v string) (T, error), def T, queryParam string) (T, error) {
	if !r.URL.Query().Has(queryParam) || r.URL.Query().Get(queryParam) == "" {
		return def, nil
	}
	str := r.URL.Query().Get(queryParam)
	return parse(str)
}