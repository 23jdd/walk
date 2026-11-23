package utils

// Difference 返回 s1 中不在 s2 里的元素（不修改 s1 和 s2）
func Difference[T comparable](s1, s2 []T) []T {
	exclude := make(map[T]struct{}, len(s2))
	for _, v := range s2 {
		exclude[v] = struct{}{}
	}

	result := make([]T, 0, len(s1))
	for _, v := range s1 {
		if _, found := exclude[v]; !found {
			result = append(result, v)
		}
	}
	return result
}

// Union 返回多个切片的并集（自动去重，保留首次出现顺序，不修改原切片）
func Union[T comparable](slicesList ...[]T) []T {
	totalLen := 0
	for _, s := range slicesList {
		totalLen += len(s)
	}

	seen := make(map[T]struct{}, totalLen)
	result := make([]T, 0, totalLen)

	for _, s := range slicesList {
		for _, v := range s {
			if _, exists := seen[v]; !exists {
				seen[v] = struct{}{}
				result = append(result, v)
			}
		}
	}
	return result
}
