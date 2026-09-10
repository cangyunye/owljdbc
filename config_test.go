package owljdbc

import (
	"reflect"
	"testing"
)

func TestDSNRoundTrip(t *testing.T) {
	in := Config{
		DriverClass: "com.oceanbase.jdbc.Driver",
		URL:         "jdbc:oceanbase://127.0.0.1:2881?useSSL=false",
		User:        "MIGSRC@oratest",
		Password:    "PASS@WORD",
		Family:      "oracle",
		Classpath:   []string{"/abs/oceanbase-client-2.4.1.jar"},
		JavaHome:    "",
		AgentJar:    "/abs/owl-agent.jar",
	}
	dsn := EncodeDSN(in)
	out, err := DecodeDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("roundtrip mismatch:\n in=%+v\nout=%+v", in, out)
	}
}
