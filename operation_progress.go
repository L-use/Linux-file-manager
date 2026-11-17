package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

type operationProgressKey struct{}

type progressWriter struct {
	io.Writer
	onWrite func(int)
}

func (w progressWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	w.onWrite(n)
	return n, err
}

func reportOperation(ctx context.Context, stage string, done, total int64) {
	if report, ok := ctx.Value(operationProgressKey{}).(func(string, int64, int64)); ok {
		report(stage, done, total)
	}
}

// Existing API clients receive their usual JSON response. The browser opts in
// to progress events followed by a final result or an application error.
func operationResponse(w http.ResponseWriter, r *http.Request, run func(context.Context) (any, error)) error {
	flusher, flushable := w.(http.Flusher)
	if !flushable || r.Header.Get("Accept") != "application/x-ndjson" {
		result, err := run(r.Context())
		if err != nil {
			return err
		}
		return respond(w, result)
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("X-Accel-Buffering", "no")
	encoder := json.NewEncoder(w)
	var last time.Time
	var stage string
	var lastTotal int64
	ctx := context.WithValue(r.Context(), operationProgressKey{}, func(next string, done, total int64) {
		if next == stage && total == lastTotal && (total <= 0 || done < total) && time.Since(last) < 150*time.Millisecond {
			return
		}
		stage, lastTotal, last = next, total, time.Now()
		_ = encoder.Encode(map[string]any{"stage": next, "done": done, "total": total})
		flusher.Flush()
	})
	result, err := run(ctx)
	if err != nil {
		p := describeProblem(err)
		_ = encoder.Encode(map[string]any{"error": p.code, "version": p.version, "replaceable": p.replaceable})
	} else {
		_ = encoder.Encode(map[string]any{"result": result})
	}
	return nil
}
