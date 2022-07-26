package sqlBuilder

import (
	"bytes"
	"fmt"
	"github.com/secure-for-ai/secureai-microsvs/db"
	"strings"
)

func (stmt *Stmt) Gen(schema ...db.Schema) (string, []interface{}, error) {
	var err error
	w := NewWriter()

	switch stmt.sqlType {
	case InsertType:
		err = stmt.insertWriteTo(w)
	case DeleteType:
		err = stmt.deleteWriteTo(w)
	case UpdateType:
		err = stmt.updateWriteTo(w)
	case SelectType:
		err = stmt.selectWriteTo(w)
	}

	sql := w.String()
	var index, i int

	index = strings.Index(sql, db.Para)

	// nothing need to be replaced
	if index < 0 {
		return sql, w.args, err
	}

	//reset memory of the writer
	w.Reset()
	w.Grow(len(sql))

	start := 0
	sepLen := len(db.Para)

	pgFunc := func() {
		fmt.Fprintf(w, "%s$%d", sql[start:start+index], i+1)
	}

	myFunc := func() {
		fmt.Fprintf(w, "%s?", sql[start:start+index])
	}

	var callback = myFunc

	if len(schema) > 0 {
		switch schema[0] {
		case db.SchPG:
			callback = pgFunc
		case db.SchMYSQL:
			w.Grow(len(sql) - len(w.args))
		}
	}

	for i = 0; ; i++ {
		if index == -1 {
			fmt.Fprintf(w, "%s", sql[start:])
			break
		}
		callback()
		start = start + index + sepLen
		index = strings.Index(sql[start:], db.Para)
	}

	return w.String(), w.args, err
}

func (stmt *Stmt) WriteTo(w Writer) error {
	switch stmt.sqlType {
	case InsertType:
		return stmt.insertWriteTo(w)
	case DeleteType:
		return stmt.deleteWriteTo(w)
	case UpdateType:
		return stmt.updateWriteTo(w)
	case SelectType:
		return stmt.selectWriteTo(w)
	}
	return ErrNotSupportType
}

func (stmt *Stmt) insertSelectWriteTo(w Writer) error {
	if _, err := fmt.Fprintf(w, "INSERT INTO %s ", stmt.tableInto); err != nil {
		return err
	}

	if len(stmt.InsertCols) > 0 {
		fmt.Fprintf(w, "(")
		fmt.Fprintf(w, strings.Join(stmt.InsertCols, ","))
		fmt.Fprintf(w, ") ")
	}

	if stmt.insertSelect != nil {
		return stmt.insertSelect.selectWriteTo(w)
	}

	return stmt.selectWriteTo(w)
}

func (stmt *Stmt) insertWriteTo(w Writer) error {
	if len(stmt.tableInto) <= 0 {
		return ErrNoTableName
	}
	if len(stmt.InsertCols) <= 0 && len(stmt.tableFrom) == 0 {
		return ErrNoColumnToInsert
	}

	// Insert Select
	if stmt.tableInto != "" && len(stmt.tableFrom) > 0 {
		return stmt.insertSelectWriteTo(w)
	}

	if len(stmt.InsertCols) > 0 {
		if _, err := fmt.Fprintf(w, "INSERT INTO %s (", stmt.tableInto); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, strings.Join(stmt.InsertCols, ",")); err != nil {
			return err
		}
		if _, err := fmt.Fprint(w, ") VALUES ("); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(w, "INSERT INTO %s VALUES (", stmt.tableInto); err != nil {
			return err
		}
	}

	switch rowsLen := len(stmt.InsertValues); rowsLen {
	case 0:
		return ErrNoValueToInsert
	default:
		values := stmt.InsertValues[0]
		valuesLen := len(values)
		args := make([]interface{}, 0, valuesLen)

		var bs []byte
		var valBuf = bytes.NewBuffer(bs)
		valBuf.Grow(valuesLen*2 - 1)

		for i, value := range values {
			if e, ok := value.(expr); ok {
				if _, err := fmt.Fprintf(valBuf, "%s", e.sql); err != nil {
					return err
				}
				args = append(args, e.args...)
			} else if value == nil {
				if _, err := fmt.Fprintf(valBuf, `null`); err != nil {
					return err
				}
			} else {
				if _, err := fmt.Fprint(valBuf, db.Para); err != nil {
					return err
				}
				args = append(args, value)
			}

			if i != valuesLen-1 {
				if _, err := fmt.Fprint(valBuf, ","); err != nil {
					return err
				}
			}
		}

		if _, err := w.Write(valBuf.Bytes()); err != nil {
			return err
		}

		if rowsLen == 1 {
			// insert one row
			w.Append(args...)
		} else {
			// insert multiple row
			w.Append(args)
			for _, values := range stmt.InsertValues[1:] {
				args = make([]interface{}, 0, valuesLen)
				for _, value := range values {
					if e, ok := value.(expr); ok {
						args = append(args, e.args...)
					} else if value == nil {
						continue
					} else {
						args = append(args, value)
					}
				}
				w.Append(args)
			}
		}
	}

	if _, err := fmt.Fprint(w, ")"); err != nil {
		return err
	}

	return nil
}

func (stmt *Stmt) deleteWriteTo(w Writer) error {
	if len(stmt.tableFrom) <= 0 {
		return ErrNoTableName
	}

	if _, err := fmt.Fprintf(w, "DELETE FROM "); err != nil {
		return err
	}

	if err := stmt.tableFrom[0].writeTo(w); err != nil {
		return err
	}

	if stmt.cond.IsValid() {
		if _, err := fmt.Fprint(w, " WHERE "); err != nil {
			return err
		}
		return stmt.cond.WriteTo(w)
	}

	return nil
}

func (stmt *Stmt) updateWriteTo(w Writer) error {
	if len(stmt.tableFrom) <= 0 {
		return ErrNoTableName
	}

	if _, err := fmt.Fprint(w, "UPDATE "); err != nil {
		return err
	}

	if err := stmt.tableFrom[0].writeTo(w); err != nil {
		return err
	}

	if _, err := fmt.Fprint(w, " SET "); err != nil {
		return err
	}

	if err := stmt.SetCols.writeNameArgs(w); err != nil {
		return err
	}

	if stmt.cond.IsValid() {
		if _, err := fmt.Fprint(w, " WHERE "); err != nil {
			return err
		}
		return stmt.cond.WriteTo(w)
	}

	return nil
}

func (stmt *Stmt) selectWriteTo(w Writer) error {
	if len(stmt.tableFrom) <= 0 {
		return ErrNoTableName
	}

	if _, err := fmt.Fprint(w, "SELECT "); err != nil {
		return err
	}

	if len(stmt.SelectCols) > 0 {
		if _, err := fmt.Fprint(w, strings.Join(stmt.SelectCols, ",")); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprint(w, "*"); err != nil {
			return err
		}
	}

	if _, err := fmt.Fprint(w, " FROM "); err != nil {
		return err
	}

	for i, from := range stmt.tableFrom {
		if err := from.writeTo(w); err != nil {
			return err
		}
		if i != len(stmt.tableFrom)-1 {
			fmt.Fprint(w, ",")
		}
	}

	if stmt.cond.IsValid() {
		if _, err := fmt.Fprint(w, " WHERE "); err != nil {
			return err
		}
		if err := stmt.cond.WriteTo(w); err != nil {
			return err
		}
	}

	if len(stmt.GroupByStr) > 0 {
		if _, err := fmt.Fprint(w, " GROUP BY ", stmt.GroupByStr); err != nil {
			return err
		}
	}

	if stmt.having.IsValid() {
		if _, err := fmt.Fprint(w, " HAVING "); err != nil {
			return err
		}
		if err := stmt.having.WriteTo(w); err != nil {
			return err
		}
	}

	if len(stmt.OrderByStr) > 0 {
		if _, err := fmt.Fprint(w, " ORDER BY ", stmt.OrderByStr); err != nil {
			return err
		}
	}

	if stmt.LimitN < 0 || stmt.Offset < 0 {
		return ErrInvalidLimitation
	} else if stmt.LimitN > 0 {
		if stmt.Offset == 0 {
			fmt.Fprint(w, " LIMIT ", stmt.LimitN)
		} else {
			fmt.Fprintf(w, " LIMIT %v OFFSET %v", stmt.LimitN, stmt.Offset)
		}
	}

	return nil
}
