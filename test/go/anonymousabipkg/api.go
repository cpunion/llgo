package anonymousabipkg

type Payload = struct {
	R0 uint32
	R1 string
}

type Transformer interface {
	ApplyAnon(Payload) Payload
}

func Call(value Transformer, input Payload) Payload {
	return value.ApplyAnon(input)
}
