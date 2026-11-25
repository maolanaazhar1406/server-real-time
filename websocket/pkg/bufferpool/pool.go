package bufferpool

import "sync"

// Pool wraps sync.Pool for fixed-capacity reusable byte slices to minimize GC pressure.
type Pool struct {
	pool sync.Pool
	size int
}

// New creates a new byte slice pool whose buffers have capacity size.
func New(size int) *Pool {
	if size <= 0 {
		size = 4096
	}
	return &Pool{
		size: size,
		pool: sync.Pool{
			New: func() any {
				buf := make([]byte, size)
				return &buf
			},
		},
	}
}

// Get retrieves a buffer pointer with length equal to the pool's configured size.
func (p *Pool) Get() *[]byte {
	bp := p.pool.Get().(*[]byte)
	if cap(*bp) < p.size {
		b := make([]byte, p.size)
		return &b
	}
	*bp = (*bp)[:p.size]
	return bp
}

// Put returns a buffer pointer to the pool.
func (p *Pool) Put(bp *[]byte) {
	if bp == nil || cap(*bp) < p.size {
		return
	}
	// Do not retain oversized buffers indefinitely (> 8x standard size).
	if cap(*bp) > p.size*8 {
		return
	}
	*bp = (*bp)[:p.size]
	p.pool.Put(bp)
}
