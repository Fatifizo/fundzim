package passwords

import (
	"context"
	"testing"
)

// BenchmarkHashDefaultParams measures one Argon2id hash with the configured defaults
// (64 MiB, t=3, p=2). Sign-in latency is dominated by this cost.
func BenchmarkHashDefaultParams(b *testing.B) {
	h := NewHasher(Params{MemoryKiB: 64 * 1024, Iterations: 3, Parallelism: 2}, 4)
	ctx := context.Background()
	for i := 0; i < b.N; i++ {
		if _, err := h.Hash(ctx, "correct horse battery staple 42"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkVerifyParallel measures verification throughput with the concurrency bound of 4.
func BenchmarkVerifyParallel(b *testing.B) {
	h := NewHasher(Params{MemoryKiB: 64 * 1024, Iterations: 3, Parallelism: 2}, 4)
	ctx := context.Background()
	phc, err := h.Hash(ctx, "correct horse battery staple 42")
	if err != nil {
		b.Fatal(err)
	}
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if ok, err := h.Verify(ctx, phc, "correct horse battery staple 42"); err != nil || !ok {
				if err == ErrBusy {
					continue
				}
				b.Fatal(ok, err)
			}
		}
	})
}
