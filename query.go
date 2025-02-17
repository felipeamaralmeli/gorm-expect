package gormexpect

import (
	"database/sql/driver"
	"fmt"
	"reflect"

	"github.com/jinzhu/gorm"
	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

// QueryExpectation is returned by Expecter. It exposes a narrower API than
// Queryer to limit footguns.
type QueryExpectation interface {
	Returns(value interface{}) *Expecter
	// Error(err error) QueryExpectation
}

// SqlmockQueryExpectation implements QueryExpectation for go-sqlmock
// It gets a pointer to Expecter
type SqlmockQueryExpectation struct {
	association *MockAssociation
	parent      *Expecter
	scope       *gorm.Scope
}

// Returns accepts an out type which should either be a struct or slice. Under
// the hood, it converts a gorm model struct to sql.Rows that can be passed to
// the underlying mock db
func (q *SqlmockQueryExpectation) Returns(out interface{}) *Expecter {
	scope := (&gorm.Scope{}).New(out)
	q.scope = scope

	if out == nil {
		q.parent.noop.ReturnNilRows()
	}

	// call deferred queries, since we now know the expected out value
	q.callMethods()

	outVal := indirect(reflect.ValueOf(out))

	destQuery := q.parent.recorder.stmts[0]

	// main query always at the head of the slice
	q.parent.adapter.ExpectQuery(destQuery).Returns(q.getDestRows(out))

	if len(q.parent.recorder.stmts) > 1 {
		// subqueries are preload
		for _, subQuery := range q.parent.recorder.stmts[1:] {
			if subQuery.preload != "" {
				fmt.Printf("Preloading: %s\r\n", subQuery.preload)
				if field, ok := scope.FieldByName(subQuery.preload); ok {
					expectation := q.parent.adapter.ExpectQuery(subQuery)
					rows := q.getRelationRows(outVal.FieldByName(subQuery.preload), subQuery.preload, field.Relationship)
					expectation.Returns(rows)
				}
			}
		}
	}

	q.parent.reset()

	return q.parent
}

func (q *SqlmockQueryExpectation) getRelationRows(rVal reflect.Value, fieldName string, relation *gorm.Relationship) *sqlmock.Rows {
	var (
		rows    *sqlmock.Rows
		columns []string
	)

	switch relation.Kind {
	case "has_one":
		scope := &gorm.Scope{Value: rVal.Interface()}

		for _, field := range scope.GetModelStruct().StructFields {
			if field.IsNormal {
				columns = append(columns, field.DBName)
			}
		}

		rows = sqlmock.NewRows(columns)

		if reflect.DeepEqual(rVal.Interface(), reflect.New(rVal.Type()).Elem().Interface()) {
			return rows
		}

		// we don't have a slice
		row := getRowForFields(scope.Fields())
		rows = rows.AddRow(row...)

		return rows
	case "has_many":
		elem := rVal.Type().Elem()
		scope := &gorm.Scope{Value: reflect.New(elem).Interface()}

		for _, field := range scope.GetModelStruct().StructFields {
			if field.IsNormal {
				columns = append(columns, field.DBName)
			}
		}

		rows = sqlmock.NewRows(columns)

		if reflect.DeepEqual(rVal.Interface(), reflect.New(rVal.Type()).Elem().Interface()) {
			return rows
		}

		if rVal.Len() > 0 {
			for i := 0; i < rVal.Len(); i++ {
				scope := &gorm.Scope{Value: rVal.Index(i).Interface()}
				row := getRowForFields(scope.Fields())
				rows = rows.AddRow(row...)
			}

			return rows
		}

		return rows
	case "many_to_many":
		elem := rVal.Type().Elem()
		scope := &gorm.Scope{Value: reflect.New(elem).Interface()}
		joinTable := relation.JoinTableHandler.(*gorm.JoinTableHandler)

		for _, field := range scope.GetModelStruct().StructFields {
			if field.IsNormal {
				columns = append(columns, field.DBName)
			}
		}

		for _, key := range joinTable.Source.ForeignKeys {
			columns = append(columns, key.DBName)
		}

		for _, key := range joinTable.Destination.ForeignKeys {
			columns = append(columns, key.DBName)
		}

		rows = sqlmock.NewRows(columns)

		// we need to check for zero values
		if reflect.DeepEqual(rVal.Interface(), reflect.New(rVal.Type()).Elem().Interface()) {
			return rows
		}

		// in this case we definitely have a slice
		if rVal.Len() > 0 {
			for i := 0; i < rVal.Len(); i++ {
				scope := &gorm.Scope{Value: rVal.Index(i).Interface()}
				row := getRowForFields(scope.Fields())

				// need to append the values for join table keys
				sourcePk := q.scope.PrimaryKeyValue()
				destModelType := joinTable.Destination.ModelType
				destModelVal := reflect.New(destModelType).Interface()
				destPkVal := (&gorm.Scope{Value: destModelVal}).PrimaryKeyValue()

				row = append(row, sourcePk, destPkVal)

				rows = rows.AddRow(row...)
			}

			return rows
		}

		return rows
	default:
		return nil
	}
}

func (q *SqlmockQueryExpectation) getDestRows(out interface{}) *sqlmock.Rows {
	var columns []string
	outVal := indirect(reflect.ValueOf(out))

	if outVal.Kind() == reflect.Slice || outVal.Kind() == reflect.Struct {
		for _, field := range (&gorm.Scope{}).New(out).GetModelStruct().StructFields {
			if field.IsNormal {
				columns = append(columns, field.DBName)
			}
		}
	} else {
		columns = append(columns, "count")
	}

	rows := sqlmock.NewRows(columns)

	// short circuit if we got nil
	if outVal.Kind() == reflect.Invalid {
		return rows
	}

	// SELECT multiple rows
	switch outVal.Kind() {
	case reflect.Slice:
		outSlice := []interface{}{}

		for i := 0; i < outVal.Len(); i++ {
			outSlice = append(outSlice, outVal.Index(i).Interface())
		}

		for _, outElem := range outSlice {
			scope := &gorm.Scope{Value: outElem}
			row := getRowForFields(scope.Fields())
			rows = rows.AddRow(row...)
		}
	case reflect.Struct:
		row := getRowForFields(q.scope.Fields())
		rows = rows.AddRow(row...)

	case reflect.Int, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		count, _ := driver.DefaultParameterConverter.ConvertValue(outVal.Interface())
		rows = rows.AddRow(count)
	default:
		panic(fmt.Errorf("Can only get rows for slice, struct, int/uint, or nil. Got: %s", outVal.Kind()))
	}

	return rows
}

// callMethods is used to call deferred db.* methods. It is necessary to ensure scope.Value has a primary key, extracted from the model passed to SqlmockQueryExpectation.Returns. This is because the noop database does not return any actual rows.
func (q *SqlmockQueryExpectation) callMethods() {
	q.parent.gorm = q.parent.gorm.Set("gorm_expect:ret", q.scope.Value)
	q.parent.gorm.Callback().Query().Before("gorm:preload").Register("gorm_expect:populate_scope_val", populateScopeValueCallback)

	noop := reflect.ValueOf(q.parent.gorm)
	for methodName, args := range q.parent.callmap {
		methodVal := noop.MethodByName(methodName)

		switch method := methodVal.Interface().(type) {
		case func(interface{}) *gorm.DB:
			method(args[0])
		case func(interface{}, ...interface{}) *gorm.DB:
			method(args[0], args[1:]...)
		default:
			fmt.Println("Not a supported method signature")
		}
	}
}
