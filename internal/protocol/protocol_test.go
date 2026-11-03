package protocol

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	cases := []Message{
		Hello{Name: "anders", PubKey: []byte{1, 2, 3}},
		Challenge{Nonce: make([]byte, NonceSize)},
		Auth{Sig: []byte("signature")},
		Welcome{Name: "anders", MOTD: "hej", Users: []string{"anders", "nikolaj"}, Server: "v0.1.0"},
		Error{Code: ErrCodeNotOnline, Detail: "bob"},
		Msg{ID: "abc", From: "anders", To: "bob", Text: "hi ¯\\_(ツ)_/¯", Me: true, TS: 1700000000000},
		Typing{From: "anders"},
		Presence{Event: PresenceJoin, Name: "bob"},
	}
	for _, in := range cases {
		b, err := Encode(in)
		if err != nil {
			t.Fatalf("encode %T: %v", in, err)
		}
		if !strings.HasPrefix(string(b), `{"v":1,"kind":"`+string(in.Kind())+`"`) {
			t.Errorf("frame %s does not start with header", b)
		}
		out, err := Decode(b)
		if err != nil {
			t.Fatalf("decode %s: %v", b, err)
		}
		if !reflect.DeepEqual(in, out) {
			t.Errorf("round trip mismatch:\n in  %#v\n out %#v", in, out)
		}
	}
}

func TestEncodeEmptyPayload(t *testing.T) {
	b, err := Encode(Typing{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"v":1,"kind":"typing"}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestDecodeUnknownKindIsIgnorable(t *testing.T) {
	m, err := Decode([]byte(`{"v":1,"kind":"cipher","box":"..."}`))
	if err != nil {
		t.Fatal(err)
	}
	if u, ok := m.(Unknown); !ok || u.Kind() != KindCipher {
		t.Errorf("got %#v, want Unknown{cipher}", m)
	}
}

func TestDecodeIgnoresUnknownFields(t *testing.T) {
	m, err := Decode([]byte(`{"v":1,"kind":"msg","text":"hi","future":42}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.(Msg).Text != "hi" {
		t.Errorf("got %#v", m)
	}
}

func TestDecodeErrors(t *testing.T) {
	cases := map[string]error{
		`not json`:                      ErrMalformed,
		`{"v":1}`:                       ErrMalformed,
		`{"v":2,"kind":"msg"}`:          ErrUnsupportedVersion,
		`{"kind":"msg"}`:                ErrUnsupportedVersion,
		`{"v":1,"kind":"msg","text":5}`: ErrMalformed,
	}
	for in, want := range cases {
		_, err := Decode([]byte(in))
		if !errors.Is(err, want) {
			t.Errorf("Decode(%s) = %v, want %v", in, err, want)
		}
	}
}

func TestValidName(t *testing.T) {
	good := []string{"ab", "anders", "nikolaj_s", "a-1", "abcdefghijklmnop"}
	bad := []string{"", "a", "Anders", "with space", "abcdefghijklmnopq", "æøå", "a.b"}
	for _, s := range good {
		if !ValidName(s) {
			t.Errorf("ValidName(%q) = false", s)
		}
	}
	for _, s := range bad {
		if ValidName(s) {
			t.Errorf("ValidName(%q) = true", s)
		}
	}
}

func TestValidText(t *testing.T) {
	if ValidText("hej") != nil {
		t.Error("plain text rejected")
	}
	if ValidText("") == nil {
		t.Error("empty text accepted")
	}
	if ValidText(strings.Repeat("x", MaxTextBytes+1)) == nil {
		t.Error("oversized text accepted")
	}
	if ValidText(string([]byte{0xff, 0xfe})) == nil {
		t.Error("invalid UTF-8 accepted")
	}
}

func TestSignedMessage(t *testing.T) {
	got := SignedMessage([]byte{0xAA, 0xBB}, "pling.example", "anders")
	want := append([]byte{0xAA, 0xBB}, []byte("pling.exampleanders")...)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %x, want %x", got, want)
	}
}
