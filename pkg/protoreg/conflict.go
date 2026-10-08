// Package protoreg inspects the protobuf type registry for extension number
// collisions between independently generated packages.
//
// The protobuf runtime reports registration conflicts during package init,
// before main() runs, by writing plain text to os.Stderr (policy "warn") or by
// panicking (policy "panic", the default). Neither can go through the
// application's structured logger. Running with
// GOLANG_PROTOBUF_REGISTRATION_CONFLICT=ignore and calling
// [FindExtensionConflicts] after the logger is ready lets the process report the
// same information as structured log entries instead.
package protoreg

import (
	"cmp"
	"iter"
	"slices"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// ExtensionConflict is a field number on a containing message that more than
// one registered extension claims.
type ExtensionConflict struct {
	// Message is the full name of the extended message, e.g.
	// "google.protobuf.MethodOptions".
	Message protoreflect.FullName
	// Number is the contested extension field number.
	Number protoreflect.FieldNumber
	// Extensions holds the full names of every extension claiming Number,
	// sorted in ascending order.
	Extensions []protoreflect.FullName
}

// Equal reports whether c and other describe the same collision.
func (c ExtensionConflict) Equal(other ExtensionConflict) bool {
	return c.Message == other.Message &&
		c.Number == other.Number &&
		slices.Equal(c.Extensions, other.Extensions)
}

// FindExtensionConflicts returns every (message, field number) pair claimed by
// two or more of the given extension types, sorted by message and number.
//
// Pass protoregistry.GlobalTypes.RangeExtensions to inspect the process-wide
// registry. Under the "warn" and "ignore" conflict policies the global registry
// keeps every colliding extension under its own full name, so all of them are
// visible here even though lookups by number resolve to only one.
func FindExtensionConflicts(extensions iter.Seq[protoreflect.ExtensionType]) []ExtensionConflict {
	type key struct {
		message protoreflect.FullName
		number  protoreflect.FieldNumber
	}
	claims := make(map[key][]protoreflect.FullName)
	for xt := range extensions {
		xd := xt.TypeDescriptor()
		k := key{message: xd.ContainingMessage().FullName(), number: xd.Number()}
		claims[k] = append(claims[k], xd.FullName())
	}

	var conflicts []ExtensionConflict
	for k, names := range claims {
		if len(names) < 2 {
			continue
		}
		slices.Sort(names)
		conflicts = append(conflicts, ExtensionConflict{
			Message:    k.message,
			Number:     k.number,
			Extensions: names,
		})
	}
	slices.SortFunc(conflicts, func(a, b ExtensionConflict) int {
		return cmp.Or(cmp.Compare(a.Message, b.Message), cmp.Compare(a.Number, b.Number))
	})
	return conflicts
}
