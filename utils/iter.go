package utils

import "iter"

func Concat[V any](seqs ...iter.Seq[V]) iter.Seq[V] {
	return func(yield func(V) bool) {
		for _, seq := range seqs {
			for v := range seq {
				if !yield(v) {
					return
				}
			}
		}
	}
}
func Count[V any](seq iter.Seq[V]) int {
	n := 0
	for range seq {
		n++
	}
	return n
}
