package ratelimit

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

type DragonflyStore struct {
	Address, Password string
	DialTimeout       time.Duration
}

func (s DragonflyStore) Take(ctx context.Context, key string, limit int, window time.Duration, _ time.Time) (Result, error) {
	dialer := net.Dialer{Timeout: s.DialTimeout}
	if dialer.Timeout <= 0 {
		dialer.Timeout = 2 * time.Second
	}
	conn, err := dialer.DialContext(ctx, "tcp", s.Address)
	if err != nil {
		return Result{}, err
	}
	defer conn.Close()
	deadline, ok := ctx.Deadline()
	if ok {
		_ = conn.SetDeadline(deadline)
	}
	reader := bufio.NewReader(conn)
	if s.Password != "" {
		if err := writeRESP(conn, "AUTH", s.Password); err != nil {
			return Result{}, err
		}
		if _, err := readRESP(reader); err != nil {
			return Result{}, err
		}
	}
	script := `local c=redis.call('INCR',KEYS[1]); if c==1 then redis.call('PEXPIRE',KEYS[1],ARGV[1]); end; return {c,redis.call('PTTL',KEYS[1])}`
	if err := writeRESP(conn, "EVAL", script, "1", key, strconv.FormatInt(window.Milliseconds(), 10)); err != nil {
		return Result{}, err
	}
	value, err := readRESP(reader)
	if err != nil {
		return Result{}, err
	}
	values, ok := value.([]any)
	if !ok || len(values) != 2 {
		return Result{}, fmt.Errorf("dragonfly: malformed response")
	}
	count, ok1 := values[0].(int64)
	ttl, ok2 := values[1].(int64)
	if !ok1 || !ok2 {
		return Result{}, fmt.Errorf("dragonfly: malformed counters")
	}
	remaining := limit - int(count)
	if remaining < 0 {
		remaining = 0
	}
	return Result{Allowed: count <= int64(limit), Remaining: remaining, RetryAfter: time.Duration(ttl) * time.Millisecond}, nil
}

func writeRESP(w io.Writer, values ...string) error {
	if _, err := fmt.Fprintf(w, "*%d\r\n", len(values)); err != nil {
		return err
	}
	for _, v := range values {
		if _, err := fmt.Fprintf(w, "$%d\r\n%s\r\n", len(v), v); err != nil {
			return err
		}
	}
	return nil
}
func readRESP(r *bufio.Reader) (any, error) {
	prefix, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	switch prefix {
	case '+':
		return line, nil
	case '-':
		return nil, fmt.Errorf("dragonfly: %s", line)
	case ':':
		return strconv.ParseInt(line, 10, 64)
	case '$':
		n, err := strconv.Atoi(line)
		if err != nil {
			return nil, err
		}
		if n < 0 {
			return nil, nil
		}
		buf := make([]byte, n+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		return string(buf[:n]), nil
	case '*':
		n, err := strconv.Atoi(line)
		if err != nil {
			return nil, err
		}
		values := make([]any, n)
		for i := range values {
			values[i], err = readRESP(r)
			if err != nil {
				return nil, err
			}
		}
		return values, nil
	default:
		return nil, fmt.Errorf("dragonfly: unknown response")
	}
}
