package qs

// ownedPayload enforces the private tag/payload invariant established by constructors.
// Nil pointers and slices are valid payloads when their dynamic type matches T.
func ownedPayload[T any](value any) T {
	payload, ok := value.(T)
	if !ok {
		panic("qs: inconsistent internal payload")
	}
	return payload
}

const (
	sqlTrue  = "TRUE"
	sqlArray = "ARRAY"
)
