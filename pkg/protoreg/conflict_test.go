package protoreg_test

import (
	"fmt"
	"iter"
	"slices"
	"testing"

	"github.com/liverty-music/backend/pkg/protoreg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// extension is a minimal description of a proto2 extension declared in its own
// file under pkg.
type extension struct {
	pkg      string
	name     string
	extendee string
	number   int32
}

func TestFindExtensionConflicts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		extensions []extension
		want       []protoreg.ExtensionConflict
	}{
		{
			name:       "no extensions",
			extensions: nil,
			want:       nil,
		},
		{
			name: "distinct numbers on the same message do not conflict",
			extensions: []extension{
				{pkg: "a.v1", name: "opt", extendee: ".google.protobuf.MethodOptions", number: 50001},
				{pkg: "b.v1", name: "opt", extendee: ".google.protobuf.MethodOptions", number: 50002},
			},
			want: nil,
		},
		{
			name: "same number on different messages does not conflict",
			extensions: []extension{
				{pkg: "a.v1", name: "opt", extendee: ".google.protobuf.MethodOptions", number: 50001},
				{pkg: "b.v1", name: "opt", extendee: ".google.protobuf.FieldOptions", number: 50001},
			},
			want: nil,
		},
		{
			name: "same number on the same message conflicts",
			extensions: []extension{
				{pkg: "zitadel.v2", name: "options", extendee: ".google.protobuf.MethodOptions", number: 50001},
				{pkg: "pocketsign.v1", name: "detail_error", extendee: ".google.protobuf.MethodOptions", number: 50001},
				{pkg: "pocketsign.v1", name: "encryptable", extendee: ".google.protobuf.FieldOptions", number: 60001},
			},
			want: []protoreg.ExtensionConflict{{
				Message:    "google.protobuf.MethodOptions",
				Number:     50001,
				Extensions: []protoreflect.FullName{"pocketsign.v1.detail_error", "zitadel.v2.options"},
			}},
		},
		{
			name: "multiple conflicts are sorted by message then number",
			extensions: []extension{
				{pkg: "a.v1", name: "m2", extendee: ".google.protobuf.MethodOptions", number: 50002},
				{pkg: "b.v1", name: "m2", extendee: ".google.protobuf.MethodOptions", number: 50002},
				{pkg: "a.v1", name: "m1", extendee: ".google.protobuf.MethodOptions", number: 50001},
				{pkg: "b.v1", name: "m1", extendee: ".google.protobuf.MethodOptions", number: 50001},
				{pkg: "a.v1", name: "f1", extendee: ".google.protobuf.FieldOptions", number: 50001},
				{pkg: "b.v1", name: "f1", extendee: ".google.protobuf.FieldOptions", number: 50001},
			},
			want: []protoreg.ExtensionConflict{
				{
					Message:    "google.protobuf.FieldOptions",
					Number:     50001,
					Extensions: []protoreflect.FullName{"a.v1.f1", "b.v1.f1"},
				},
				{
					Message:    "google.protobuf.MethodOptions",
					Number:     50001,
					Extensions: []protoreflect.FullName{"a.v1.m1", "b.v1.m1"},
				},
				{
					Message:    "google.protobuf.MethodOptions",
					Number:     50002,
					Extensions: []protoreflect.FullName{"a.v1.m2", "b.v1.m2"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := protoreg.FindExtensionConflicts(newExtensionTypes(t, tt.extensions))

			assert.Equal(t, tt.want, got)
		})
	}
}

func TestExtensionConflict_Equal(t *testing.T) {
	t.Parallel()

	base := protoreg.ExtensionConflict{
		Message:    "google.protobuf.MethodOptions",
		Number:     50001,
		Extensions: []protoreflect.FullName{"a.v1.opt", "b.v1.opt"},
	}

	tests := []struct {
		name  string
		other protoreg.ExtensionConflict
		want  bool
	}{
		{
			name:  "identical",
			other: base,
			want:  true,
		},
		{
			name: "different message",
			other: protoreg.ExtensionConflict{
				Message: "google.protobuf.FieldOptions", Number: base.Number, Extensions: base.Extensions,
			},
			want: false,
		},
		{
			name: "different number",
			other: protoreg.ExtensionConflict{
				Message: base.Message, Number: 50002, Extensions: base.Extensions,
			},
			want: false,
		},
		{
			name: "additional extension",
			other: protoreg.ExtensionConflict{
				Message: base.Message, Number: base.Number,
				Extensions: []protoreflect.FullName{"a.v1.opt", "b.v1.opt", "c.v1.opt"},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, base.Equal(tt.other))
		})
	}
}

// newExtensionTypes builds a dynamic extension type for each declaration. A
// local registry rejects colliding extensions outright, so the types are
// yielded directly instead of through protoregistry.Types.RangeExtensions.
func newExtensionTypes(t *testing.T, exts []extension) iter.Seq[protoreflect.ExtensionType] {
	t.Helper()

	types := make([]protoreflect.ExtensionType, 0, len(exts))
	for i, ext := range exts {
		fdp := &descriptorpb.FileDescriptorProto{
			Name:       new(fmt.Sprintf("%s/%s_%d.proto", ext.pkg, ext.name, i)),
			Package:    new(ext.pkg),
			Dependency: []string{"google/protobuf/descriptor.proto"},
			Extension: []*descriptorpb.FieldDescriptorProto{{
				Name:     new(ext.name),
				Number:   new(ext.number),
				Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
				Extendee: new(ext.extendee),
			}},
		}
		fd, err := protodesc.NewFile(fdp, protoregistry.GlobalFiles)
		require.NoError(t, err)
		types = append(types, dynamicpb.NewExtensionType(fd.Extensions().Get(0)))
	}
	return slices.Values(types)
}
