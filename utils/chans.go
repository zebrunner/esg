package utils

func SendToChanIfNotBlocked[T interface{}](ch chan<- T, entity T) (sent bool) {
	select {
	case ch <- entity:
		sent = true
	default:
		sent = false
	}

	return sent
}
