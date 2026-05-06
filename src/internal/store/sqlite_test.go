package store

import (
	"context"
	"errors"
	"testing"

	sqlite "modernc.org/sqlite"
)

func TestDriverStillReportsTypedConstraintCodes(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	seedNode(t, s)
	seedDongle(t, s, "d1", "860000000000001")

	cases := []struct {
		name string
		code int
		sql  string
	}{
		{
			"unique", codeConstraintUnique,
			`INSERT INTO dongles(id,node_id,imei,created_at,updated_at) VALUES('d2','n1','860000000000001',1,1)`,
		},
		{
			"foreign key", codeConstraintForeignKey,
			`INSERT INTO slots(id,node_id,slot,usb_path,if_name,created_at,updated_at) VALUES('x','missing',1,'a','dg01',1,1)`,
		},
		{
			"check", codeConstraintCheck,
			`INSERT INTO dongles(id,node_id,imei,cap_reset_day,created_at,updated_at) VALUES('d3','n1','860000000000099',99,1,1)`,
		},
	}

	for _, tc := range cases {
		_, err := s.db.ExecContext(ctx, tc.sql)
		if err == nil {
			t.Fatalf("%s: the statement was accepted", tc.name)
		}
		var se *sqlite.Error
		if !errors.As(err, &se) {
			t.Fatalf("%s: driver returned an untyped %T; every constraint would collapse to a generic error", tc.name, err)
		}
		if se.Code() != tc.code {
			t.Errorf("%s: driver reported code %d, mapping expects %d", tc.name, se.Code(), tc.code)
		}
	}
}
