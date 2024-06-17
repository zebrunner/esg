package utils

import "math"

func Paginate[T interface{}](l []T, size int) [][]T {
	numPages := int(math.Ceil(float64(len(l)) / float64(size)))
	pages := make([][]T, numPages)
	for i := 0; i < numPages; i++ {
		left := i * size
		right := (i + 1) * size
		if right > len(l) {
			right = len(l)
		}
		pages[i] = l[left:right]
	}

	return pages
}
