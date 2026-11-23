package utils

import (
	"iter"
)

// Concat 按顺序拼接多个 iter.Seq（惰性求值）
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

// Count 统计 iter.Seq 中的元素总数
func Count[V any](seq iter.Seq[V]) int {
	n := 0
	for range seq {
		n++
	}
	return n
}

// CountFunc 统计满足条件的元素数量
func CountFunc[V any](seq iter.Seq[V], pred func(V) bool) int {
	n := 0
	for v := range seq {
		if pred(v) {
			n++
		}
	}
	return n
}

// Map 将序列中的每个元素映射为新类型或新值
func Map[T, R any](seq iter.Seq[T], fn func(T) R) iter.Seq[R] {
	return func(yield func(R) bool) {
		for v := range seq {
			if !yield(fn(v)) {
				return
			}
		}
	}
}

// Filter 保留满足条件的元素
func Filter[V any](seq iter.Seq[V], pred func(V) bool) iter.Seq[V] {
	return func(yield func(V) bool) {
		for v := range seq {
			if pred(v) && !yield(v) {
				return
			}
		}
	}
}

// Take 最多只取前 n 个元素（n <= 0 时返回空序列）
func Take[V any](seq iter.Seq[V], n int) iter.Seq[V] {
	return func(yield func(V) bool) {
		if n <= 0 {
			return
		}
		count := 0
		for v := range seq {
			if !yield(v) {
				return
			}
			count++
			if count >= n {
				return
			}
		}
	}
}

// Skip 跳过前 n 个元素（n <= 0 时不跳过）
func Skip[V any](seq iter.Seq[V], n int) iter.Seq[V] {
	return func(yield func(V) bool) {
		skipped := 0
		for v := range seq {
			if skipped < n {
				skipped++
				continue
			}
			if !yield(v) {
				return
			}
		}
	}
}

// Reduce 将序列归约为单个值（即时求值）
func Reduce[T, R any](seq iter.Seq[T], initial R, fn func(acc R, cur T) R) R {
	acc := initial
	for v := range seq {
		acc = fn(acc, v)
	}
	return acc
}

// Any 判断序列中是否存在至少一个满足条件的元素（命中即短路停止）
func Any[V any](seq iter.Seq[V], pred func(V) bool) bool {
	for v := range seq {
		if pred(v) {
			return true
		}
	}
	return false
}

// All 判断序列中是否所有元素都满足条件（遇到不满足即短路停止）
func All[V any](seq iter.Seq[V], pred func(V) bool) bool {
	for v := range seq {
		if !pred(v) {
			return false
		}
	}
	return true
}

// First 返回第一个匹配的元素及是否找到
func First[V any](seq iter.Seq[V], pred func(V) bool) (V, bool) {
	for v := range seq {
		if pred(v) {
			return v, true
		}
	}
	var zero V
	return zero, false
}

// Enumerate 将 iter.Seq[V] 转换为带索引的 iter.Seq2[int, V]
func Enumerate[V any](seq iter.Seq[V]) iter.Seq2[int, V] {
	return func(yield func(int, V) bool) {
		idx := 0
		for v := range seq {
			if !yield(idx, v) {
				return
			}
			idx++
		}
	}
}

// Intersect 计算多个 iter.Seq 的交集（按 seqs[0] 顺序输出，结果去重）
func Intersect[V comparable](seqs ...iter.Seq[V]) iter.Seq[V] {
	return func(yield func(V) bool) {
		if len(seqs) == 0 {
			return
		}
		if len(seqs) == 1 {
			seen := make(map[V]struct{})
			for v := range seqs[0] {
				if _, ok := seen[v]; ok {
					continue
				}
				seen[v] = struct{}{}
				if !yield(v) {
					return
				}
			}
			return
		}

		// 为 seqs[1:] 各自建立集合，只要有一个序列为空，最终交集必为空
		otherSets := make([]map[V]struct{}, len(seqs)-1)
		for i, seq := range seqs[1:] {
			s := make(map[V]struct{})
			for v := range seq {
				s[v] = struct{}{}
			}
			if len(s) == 0 {
				return
			}
			otherSets[i] = s
		}

		emitted := make(map[V]struct{})
		for v := range seqs[0] {
			if _, ok := emitted[v]; ok {
				continue
			}
			if !inAllSets(v, otherSets) {
				continue
			}
			emitted[v] = struct{}{}
			if !yield(v) {
				return
			}
		}
	}
}

func inAllSets[V comparable](v V, sets []map[V]struct{}) bool {
	for _, s := range sets {
		if _, ok := s[v]; !ok {
			return false
		}
	}
	return true
}

// IntersectBy 按 keyFn 提取的键计算多个 iter.Seq 的交集（保留 seqs[0] 中首次匹配的元素）
func IntersectBy[V any, K comparable](keyFn func(V) K, seqs ...iter.Seq[V]) iter.Seq[V] {
	return func(yield func(V) bool) {
		if len(seqs) == 0 {
			return
		}
		if len(seqs) == 1 {
			seen := make(map[K]struct{})
			for v := range seqs[0] {
				k := keyFn(v)
				if _, ok := seen[k]; ok {
					continue
				}
				seen[k] = struct{}{}
				if !yield(v) {
					return
				}
			}
			return
		}

		otherSets := make([]map[K]struct{}, len(seqs)-1)
		for i, seq := range seqs[1:] {
			s := make(map[K]struct{})
			for v := range seq {
				s[keyFn(v)] = struct{}{}
			}
			if len(s) == 0 {
				return
			}
			otherSets[i] = s
		}

		emitted := make(map[K]struct{})
		for v := range seqs[0] {
			k := keyFn(v)
			if _, ok := emitted[k]; ok {
				continue
			}
			if !inAllSets(k, otherSets) {
				continue
			}
			emitted[k] = struct{}{}
			if !yield(v) {
				return
			}
		}
	}
}
