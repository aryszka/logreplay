package logreplay

import (
	"io"
	"math/rand"
)

const chars = "      abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

type randomReader struct{}

func (r randomReader) Read(p []byte) (int, error) {
	for i := 0; i < len(p); i++ {
		p[i] = randomByte()
	}

	return len(p), nil
}

func randomByte() byte {
	return chars[rand.Intn(len(chars))]
}

func deviate(i int, d float64) int {
	di := int(float64(i) * d)
	return i + rand.Intn(2*di) - di
}

func deviateMin(i int, d float64) int {
	i = deviate(i, d)
	if i < 0 {
		i = 0
	}

	return i
}

func randomText(n int) io.Reader {
	return io.LimitReader(randomReader{}, int64(n))
}
