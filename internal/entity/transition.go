package entity

// Transition is a state change an entity decided: the state it moves to and
// the states it may move from. A repository applies it with a compare-and-set
// (`UPDATE … WHERE state = ANY(From)`), so a row that a concurrent writer
// already moved is not overwritten. The zero Transition changes nothing.
type Transition[S comparable] struct {
	// From lists the states the row may be in for the change to apply.
	From []S
	// To is the state the row moves to.
	To S
}

// Changed reports whether the transition changes anything; the zero
// Transition does not.
func (t Transition[S]) Changed() bool {
	return len(t.From) > 0
}
