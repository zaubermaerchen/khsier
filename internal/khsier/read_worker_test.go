package khsier

// This file verifies the persistent idle-mode reader worker contract.

import (
	"io"
	"testing"
)

func TestReadWorkerReusesRequestAndResultChannels(t *testing.T) {
	reader := &workerReader{}
	worker := newReadWorker(reader)
	t.Cleanup(worker.stop)
	requestChannel := worker.requests
	resultChannel := worker.results
	buffer := make([]byte, 8)

	worker.request(buffer)
	first := <-resultChannel
	if first.n != 1 || first.err != nil || string(buffer[:first.n]) != "a" {
		t.Fatalf("first result = %#v, buffer = %q", first, buffer[:first.n])
	}
	worker.request(buffer)
	second := <-resultChannel
	if second.n != 1 || second.err != io.EOF || string(buffer[:second.n]) != "b" {
		t.Fatalf("second result = %#v, buffer = %q", second, buffer[:second.n])
	}
	if worker.requests != requestChannel || worker.results != resultChannel {
		t.Fatal("read worker replaced its request or result channel")
	}
}

type workerReader struct{ calls int }

func (r *workerReader) Read(p []byte) (int, error) {
	r.calls++
	p[0] = byte('a' + r.calls - 1)
	if r.calls == 2 {
		return 1, io.EOF
	}
	return 1, nil
}
