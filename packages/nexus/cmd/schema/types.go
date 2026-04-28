package main

type Schema struct {
	Version       string                `json:"version"`
	Methods       []Method              `json:"methods"`
	Notifications []Notification        `json:"notifications"`
	Definitions   map[string]TypeSchema `json:"definitions"`
}

type Method struct {
	Name     string     `json:"name"`
	Request  TypeSchema `json:"request"`
	Response TypeSchema `json:"response"`
}

type Notification struct {
	Name   string     `json:"name"`
	Params TypeSchema `json:"params"`
}

type TypeSchema struct {
	Type       string                `json:"type"`
	Properties map[string]PropSchema `json:"properties,omitempty"`
	Items      *TypeSchema           `json:"items,omitempty"`
	Ref        string                `json:"$ref,omitempty"`
}

type PropSchema struct {
	Type     string      `json:"type,omitempty"`
	Ref      string      `json:"$ref,omitempty"`
	Items    *TypeSchema `json:"items,omitempty"`
	Optional bool        `json:"optional,omitempty"`
}

func str(optional bool) PropSchema       { return PropSchema{Type: "string", Optional: optional} }
func integer(optional bool) PropSchema   { return PropSchema{Type: "int", Optional: optional} }
func boolean(optional bool) PropSchema   { return PropSchema{Type: "bool", Optional: optional} }
func timestamp(optional bool) PropSchema { return PropSchema{Type: "time", Optional: optional} }
func ref(name string, optional bool) PropSchema {
	return PropSchema{Ref: "#/definitions/" + name, Optional: optional}
}
func refType(name string) TypeSchema {
	return TypeSchema{Ref: "#/definitions/" + name}
}
func arrayOf(name string, optional bool) PropSchema {
	return PropSchema{
		Type:     "array",
		Items:    &TypeSchema{Ref: "#/definitions/" + name},
		Optional: optional,
	}
}
func arrayOfType(t string, optional bool) PropSchema {
	return PropSchema{
		Type:     "array",
		Items:    &TypeSchema{Type: t},
		Optional: optional,
	}
}

func obj(props map[string]PropSchema) TypeSchema {
	return TypeSchema{Type: "object", Properties: props}
}

func emptyObj() TypeSchema {
	return TypeSchema{Type: "object"}
}
