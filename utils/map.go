package utils

// DeleteKeysBySlice 从 map 中删除 keys 切片包含的所有键（原地修改）
func DeleteKeysBySlice[K ~string, V any](m map[K]V, keys []K) {
	if m == nil {
		return
	}
	for _, k := range keys {
		delete(m, k)
	}
}
