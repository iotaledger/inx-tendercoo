// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.28.0
// 	protoc        v3.21.1
// source: tx.proto

package tendermint

import (
	reflect "reflect"
	sync "sync"

	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

// TxRaw contains a signed transaction essence.
type TxRaw struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// raw bytes of the transaction essence
	Essence []byte `protobuf:"bytes,1,opt,name=essence,proto3" json:"essence,omitempty"`
	// plain Ed25519 public key of the issuer
	PublicKey []byte `protobuf:"bytes,2,opt,name=public_key,json=publicKey,proto3" json:"public_key,omitempty"`
	// plain Ed25519 signature of essence
	Signature []byte `protobuf:"bytes,3,opt,name=signature,proto3" json:"signature,omitempty"`
}

func (x *TxRaw) Reset() {
	*x = TxRaw{}
	if protoimpl.UnsafeEnabled {
		mi := &file_tx_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *TxRaw) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*TxRaw) ProtoMessage() {}

func (x *TxRaw) ProtoReflect() protoreflect.Message {
	mi := &file_tx_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use TxRaw.ProtoReflect.Descriptor instead.
func (*TxRaw) Descriptor() ([]byte, []int) {
	return file_tx_proto_rawDescGZIP(), []int{0}
}

func (x *TxRaw) GetEssence() []byte {
	if x != nil {
		return x.Essence
	}
	return nil
}

func (x *TxRaw) GetPublicKey() []byte {
	if x != nil {
		return x.PublicKey
	}
	return nil
}

func (x *TxRaw) GetSignature() []byte {
	if x != nil {
		return x.Signature
	}
	return nil
}

// Parent represents a milestone parent proposal.
type Parent struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// index of the corresponding milestone to prevent replays
	Index uint32 `protobuf:"varint,1,opt,name=index,proto3" json:"index,omitempty"`
	// block ID of the proposed message
	BlockId []byte `protobuf:"bytes,2,opt,name=block_id,json=blockId,proto3" json:"block_id,omitempty"`
}

func (x *Parent) Reset() {
	*x = Parent{}
	if protoimpl.UnsafeEnabled {
		mi := &file_tx_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Parent) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Parent) ProtoMessage() {}

func (x *Parent) ProtoReflect() protoreflect.Message {
	mi := &file_tx_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Parent.ProtoReflect.Descriptor instead.
func (*Parent) Descriptor() ([]byte, []int) {
	return file_tx_proto_rawDescGZIP(), []int{1}
}

func (x *Parent) GetIndex() uint32 {
	if x != nil {
		return x.Index
	}
	return 0
}

func (x *Parent) GetBlockId() []byte {
	if x != nil {
		return x.BlockId
	}
	return nil
}

// Proof that a certain pre-milestone has been received.
type Proof struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// index of the corresponding milestone to prevent replays
	Index uint32 `protobuf:"varint,1,opt,name=index,proto3" json:"index,omitempty"`
	// block ID of a parent
	ParentBlockId []byte `protobuf:"bytes,2,opt,name=parent_block_id,json=parentBlockId,proto3" json:"parent_block_id,omitempty"`
}

func (x *Proof) Reset() {
	*x = Proof{}
	if protoimpl.UnsafeEnabled {
		mi := &file_tx_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Proof) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Proof) ProtoMessage() {}

func (x *Proof) ProtoReflect() protoreflect.Message {
	mi := &file_tx_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Proof.ProtoReflect.Descriptor instead.
func (*Proof) Descriptor() ([]byte, []int) {
	return file_tx_proto_rawDescGZIP(), []int{2}
}

func (x *Proof) GetIndex() uint32 {
	if x != nil {
		return x.Index
	}
	return 0
}

func (x *Proof) GetParentBlockId() []byte {
	if x != nil {
		return x.ParentBlockId
	}
	return nil
}

// PartialSignature of a given milestone.
type PartialSignature struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// plain Ed25519 signature of the implicit milestone essence
	Signature []byte `protobuf:"bytes,1,opt,name=signature,proto3" json:"signature,omitempty"`
}

func (x *PartialSignature) Reset() {
	*x = PartialSignature{}
	if protoimpl.UnsafeEnabled {
		mi := &file_tx_proto_msgTypes[3]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *PartialSignature) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PartialSignature) ProtoMessage() {}

func (x *PartialSignature) ProtoReflect() protoreflect.Message {
	mi := &file_tx_proto_msgTypes[3]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PartialSignature.ProtoReflect.Descriptor instead.
func (*PartialSignature) Descriptor() ([]byte, []int) {
	return file_tx_proto_rawDescGZIP(), []int{3}
}

func (x *PartialSignature) GetSignature() []byte {
	if x != nil {
		return x.Signature
	}
	return nil
}

// Essence of a TxRaw.
type Essence struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Types that are assignable to Message:
	//	*Essence_Parent
	//	*Essence_Proof
	//	*Essence_PartialSignature
	Message isEssence_Message `protobuf_oneof:"message"`
}

func (x *Essence) Reset() {
	*x = Essence{}
	if protoimpl.UnsafeEnabled {
		mi := &file_tx_proto_msgTypes[4]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Essence) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Essence) ProtoMessage() {}

func (x *Essence) ProtoReflect() protoreflect.Message {
	mi := &file_tx_proto_msgTypes[4]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Essence.ProtoReflect.Descriptor instead.
func (*Essence) Descriptor() ([]byte, []int) {
	return file_tx_proto_rawDescGZIP(), []int{4}
}

func (m *Essence) GetMessage() isEssence_Message {
	if m != nil {
		return m.Message
	}
	return nil
}

func (x *Essence) GetParent() *Parent {
	if x, ok := x.GetMessage().(*Essence_Parent); ok {
		return x.Parent
	}
	return nil
}

func (x *Essence) GetProof() *Proof {
	if x, ok := x.GetMessage().(*Essence_Proof); ok {
		return x.Proof
	}
	return nil
}

func (x *Essence) GetPartialSignature() *PartialSignature {
	if x, ok := x.GetMessage().(*Essence_PartialSignature); ok {
		return x.PartialSignature
	}
	return nil
}

type isEssence_Message interface {
	isEssence_Message()
}

type Essence_Parent struct {
	Parent *Parent `protobuf:"bytes,1,opt,name=parent,proto3,oneof"`
}

type Essence_Proof struct {
	Proof *Proof `protobuf:"bytes,2,opt,name=proof,proto3,oneof"`
}

type Essence_PartialSignature struct {
	PartialSignature *PartialSignature `protobuf:"bytes,3,opt,name=partial_signature,json=partialSignature,proto3,oneof"`
}

func (*Essence_Parent) isEssence_Message() {}

func (*Essence_Proof) isEssence_Message() {}

func (*Essence_PartialSignature) isEssence_Message() {}

var File_tx_proto protoreflect.FileDescriptor

var file_tx_proto_rawDesc = []byte{
	0x0a, 0x08, 0x74, 0x78, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x0a, 0x74, 0x65, 0x6e, 0x64,
	0x65, 0x72, 0x6d, 0x69, 0x6e, 0x74, 0x22, 0x5e, 0x0a, 0x05, 0x54, 0x78, 0x52, 0x61, 0x77, 0x12,
	0x18, 0x0a, 0x07, 0x65, 0x73, 0x73, 0x65, 0x6e, 0x63, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0c,
	0x52, 0x07, 0x65, 0x73, 0x73, 0x65, 0x6e, 0x63, 0x65, 0x12, 0x1d, 0x0a, 0x0a, 0x70, 0x75, 0x62,
	0x6c, 0x69, 0x63, 0x5f, 0x6b, 0x65, 0x79, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0c, 0x52, 0x09, 0x70,
	0x75, 0x62, 0x6c, 0x69, 0x63, 0x4b, 0x65, 0x79, 0x12, 0x1c, 0x0a, 0x09, 0x73, 0x69, 0x67, 0x6e,
	0x61, 0x74, 0x75, 0x72, 0x65, 0x18, 0x03, 0x20, 0x01, 0x28, 0x0c, 0x52, 0x09, 0x73, 0x69, 0x67,
	0x6e, 0x61, 0x74, 0x75, 0x72, 0x65, 0x22, 0x39, 0x0a, 0x06, 0x50, 0x61, 0x72, 0x65, 0x6e, 0x74,
	0x12, 0x14, 0x0a, 0x05, 0x69, 0x6e, 0x64, 0x65, 0x78, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0d, 0x52,
	0x05, 0x69, 0x6e, 0x64, 0x65, 0x78, 0x12, 0x19, 0x0a, 0x08, 0x62, 0x6c, 0x6f, 0x63, 0x6b, 0x5f,
	0x69, 0x64, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0c, 0x52, 0x07, 0x62, 0x6c, 0x6f, 0x63, 0x6b, 0x49,
	0x64, 0x22, 0x45, 0x0a, 0x05, 0x50, 0x72, 0x6f, 0x6f, 0x66, 0x12, 0x14, 0x0a, 0x05, 0x69, 0x6e,
	0x64, 0x65, 0x78, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0d, 0x52, 0x05, 0x69, 0x6e, 0x64, 0x65, 0x78,
	0x12, 0x26, 0x0a, 0x0f, 0x70, 0x61, 0x72, 0x65, 0x6e, 0x74, 0x5f, 0x62, 0x6c, 0x6f, 0x63, 0x6b,
	0x5f, 0x69, 0x64, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0c, 0x52, 0x0d, 0x70, 0x61, 0x72, 0x65, 0x6e,
	0x74, 0x42, 0x6c, 0x6f, 0x63, 0x6b, 0x49, 0x64, 0x22, 0x30, 0x0a, 0x10, 0x50, 0x61, 0x72, 0x74,
	0x69, 0x61, 0x6c, 0x53, 0x69, 0x67, 0x6e, 0x61, 0x74, 0x75, 0x72, 0x65, 0x12, 0x1c, 0x0a, 0x09,
	0x73, 0x69, 0x67, 0x6e, 0x61, 0x74, 0x75, 0x72, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0c, 0x52,
	0x09, 0x73, 0x69, 0x67, 0x6e, 0x61, 0x74, 0x75, 0x72, 0x65, 0x22, 0xba, 0x01, 0x0a, 0x07, 0x45,
	0x73, 0x73, 0x65, 0x6e, 0x63, 0x65, 0x12, 0x2c, 0x0a, 0x06, 0x70, 0x61, 0x72, 0x65, 0x6e, 0x74,
	0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x12, 0x2e, 0x74, 0x65, 0x6e, 0x64, 0x65, 0x72, 0x6d,
	0x69, 0x6e, 0x74, 0x2e, 0x50, 0x61, 0x72, 0x65, 0x6e, 0x74, 0x48, 0x00, 0x52, 0x06, 0x70, 0x61,
	0x72, 0x65, 0x6e, 0x74, 0x12, 0x29, 0x0a, 0x05, 0x70, 0x72, 0x6f, 0x6f, 0x66, 0x18, 0x02, 0x20,
	0x01, 0x28, 0x0b, 0x32, 0x11, 0x2e, 0x74, 0x65, 0x6e, 0x64, 0x65, 0x72, 0x6d, 0x69, 0x6e, 0x74,
	0x2e, 0x50, 0x72, 0x6f, 0x6f, 0x66, 0x48, 0x00, 0x52, 0x05, 0x70, 0x72, 0x6f, 0x6f, 0x66, 0x12,
	0x4b, 0x0a, 0x11, 0x70, 0x61, 0x72, 0x74, 0x69, 0x61, 0x6c, 0x5f, 0x73, 0x69, 0x67, 0x6e, 0x61,
	0x74, 0x75, 0x72, 0x65, 0x18, 0x03, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x1c, 0x2e, 0x74, 0x65, 0x6e,
	0x64, 0x65, 0x72, 0x6d, 0x69, 0x6e, 0x74, 0x2e, 0x50, 0x61, 0x72, 0x74, 0x69, 0x61, 0x6c, 0x53,
	0x69, 0x67, 0x6e, 0x61, 0x74, 0x75, 0x72, 0x65, 0x48, 0x00, 0x52, 0x10, 0x70, 0x61, 0x72, 0x74,
	0x69, 0x61, 0x6c, 0x53, 0x69, 0x67, 0x6e, 0x61, 0x74, 0x75, 0x72, 0x65, 0x42, 0x09, 0x0a, 0x07,
	0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x42, 0x32, 0x5a, 0x30, 0x67, 0x69, 0x74, 0x68, 0x75,
	0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x69, 0x6f, 0x74, 0x61, 0x6c, 0x65, 0x64, 0x67, 0x65, 0x72,
	0x2f, 0x74, 0x65, 0x6e, 0x64, 0x65, 0x72, 0x63, 0x6f, 0x6f, 0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f,
	0x2f, 0x74, 0x65, 0x6e, 0x64, 0x65, 0x72, 0x6d, 0x69, 0x6e, 0x74, 0x62, 0x06, 0x70, 0x72, 0x6f,
	0x74, 0x6f, 0x33,
}

var (
	file_tx_proto_rawDescOnce sync.Once
	file_tx_proto_rawDescData = file_tx_proto_rawDesc
)

func file_tx_proto_rawDescGZIP() []byte {
	file_tx_proto_rawDescOnce.Do(func() {
		file_tx_proto_rawDescData = protoimpl.X.CompressGZIP(file_tx_proto_rawDescData)
	})
	return file_tx_proto_rawDescData
}

var file_tx_proto_msgTypes = make([]protoimpl.MessageInfo, 5)
var file_tx_proto_goTypes = []interface{}{
	(*TxRaw)(nil),            // 0: tendermint.TxRaw
	(*Parent)(nil),           // 1: tendermint.Parent
	(*Proof)(nil),            // 2: tendermint.Proof
	(*PartialSignature)(nil), // 3: tendermint.PartialSignature
	(*Essence)(nil),          // 4: tendermint.Essence
}
var file_tx_proto_depIdxs = []int32{
	1, // 0: tendermint.Essence.parent:type_name -> tendermint.Parent
	2, // 1: tendermint.Essence.proof:type_name -> tendermint.Proof
	3, // 2: tendermint.Essence.partial_signature:type_name -> tendermint.PartialSignature
	3, // [3:3] is the sub-list for method output_type
	3, // [3:3] is the sub-list for method input_type
	3, // [3:3] is the sub-list for extension type_name
	3, // [3:3] is the sub-list for extension extendee
	0, // [0:3] is the sub-list for field type_name
}

func init() { file_tx_proto_init() }
func file_tx_proto_init() {
	if File_tx_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_tx_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*TxRaw); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_tx_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Parent); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_tx_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Proof); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_tx_proto_msgTypes[3].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*PartialSignature); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_tx_proto_msgTypes[4].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Essence); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
	}
	file_tx_proto_msgTypes[4].OneofWrappers = []interface{}{
		(*Essence_Parent)(nil),
		(*Essence_Proof)(nil),
		(*Essence_PartialSignature)(nil),
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_tx_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   5,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_tx_proto_goTypes,
		DependencyIndexes: file_tx_proto_depIdxs,
		MessageInfos:      file_tx_proto_msgTypes,
	}.Build()
	File_tx_proto = out.File
	file_tx_proto_rawDesc = nil
	file_tx_proto_goTypes = nil
	file_tx_proto_depIdxs = nil
}
