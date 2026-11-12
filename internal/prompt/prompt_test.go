package prompt

import (
	"errors"
	"testing"
)

func TestChainPrefersTheFirstChannelThatAnswers(t *testing.T) {
	calls := []string{}
	f := chain(
		func(string) (string, error) { calls = append(calls, "env"); return "", ErrUnavailable },
		func(string) (string, error) { calls = append(calls, "tty"); return "from-tty", nil },
		func(string) (string, error) { calls = append(calls, "askpass"); return "from-askpass", nil },
	)
	pw, err := f("master password")
	if err != nil || pw != "from-tty" {
		t.Fatalf("got %q, %v", pw, err)
	}
	if len(calls) != 2 || calls[1] != "tty" {
		t.Fatalf("calls = %v", calls)
	}
}

func TestChainSkipsUnavailableChannels(t *testing.T) {
	f := chain(
		func(string) (string, error) { return "", ErrUnavailable },
		nil,
		func(string) (string, error) { return "last", nil },
	)
	if pw, err := f("master password"); err != nil || pw != "last" {
		t.Fatalf("got %q, %v", pw, err)
	}
}

func TestChainReportsWhyNothingWorked(t *testing.T) {
	f := chain(func(string) (string, error) { return "", ErrUnavailable })
	if _, err := f("master password"); !errors.Is(err, ErrNoChannel) {
		t.Fatalf("expected ErrNoChannel, got %v", err)
	}
}

func TestChainRejectsEmptyPasswords(t *testing.T) {
	f := chain(
		func(string) (string, error) { return "", nil },
		func(string) (string, error) { return "  ", nil },
	)
	if _, err := f("master password"); !errors.Is(err, ErrNoChannel) {
		t.Fatalf("expected ErrNoChannel, got %v", err)
	}
}

func TestEnvChannel(t *testing.T) {
	t.Setenv("AISSH_MASTER_PASSWORD", "secret")
	if pw, err := Env()("master password"); err != nil || pw != "secret" {
		t.Fatalf("got %q, %v", pw, err)
	}
	t.Setenv("AISSH_MASTER_PASSWORD", "")
	if _, err := Env()("master password"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}
