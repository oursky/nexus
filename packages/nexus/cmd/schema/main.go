package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

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

func buildSchema() Schema {
	return Schema{
		Version: "1",
		Methods: []Method{
			// ── workspace ────────────────────────────────────────────────────────
			{
				Name: "workspace.create",
				Request: obj(map[string]PropSchema{
					"spec": ref("CreateSpec", false),
				}),
				Response: obj(map[string]PropSchema{
					"workspace": ref("Workspace", false),
				}),
			},
			{
				Name:    "workspace.list",
				Request: emptyObj(),
				Response: obj(map[string]PropSchema{
					"workspaces": arrayOf("Workspace", false),
				}),
			},
			{
				Name: "workspace.info",
				Request: obj(map[string]PropSchema{
					"id": str(false),
				}),
				Response: obj(map[string]PropSchema{
					"workspace": ref("Workspace", false),
				}),
			},
			{
				Name: "workspace.remove",
				Request: obj(map[string]PropSchema{
					"id": str(false),
				}),
				Response: obj(map[string]PropSchema{
					"removed": boolean(false),
				}),
			},
			{
				Name: "workspace.start",
				Request: obj(map[string]PropSchema{
					"id": str(false),
				}),
				Response: obj(map[string]PropSchema{
					"workspace": ref("Workspace", false),
				}),
			},
			{
				Name: "workspace.stop",
				Request: obj(map[string]PropSchema{
					"id": str(false),
				}),
				Response: obj(map[string]PropSchema{
					"stopped":   boolean(false),
					"workspace": ref("Workspace", true),
				}),
			},
			{
				Name: "workspace.restore",
				Request: obj(map[string]PropSchema{
					"id": str(false),
				}),
				Response: obj(map[string]PropSchema{
					"restored":  boolean(false),
					"workspace": ref("Workspace", true),
				}),
			},
			{
				Name: "workspace.fork",
				Request: obj(map[string]PropSchema{
					"id":                 str(false),
					"childWorkspaceName": str(true),
					"childRef":           str(false),
				}),
				Response: obj(map[string]PropSchema{
					"forked":    boolean(false),
					"workspace": ref("Workspace", true),
				}),
			},
			{
				Name: "workspace.ready",
				Request: obj(map[string]PropSchema{
					"id": str(false),
				}),
				Response: obj(map[string]PropSchema{
					"ready": boolean(false),
				}),
			},
			{
				Name: "workspace.ports.list",
				Request: obj(map[string]PropSchema{
					"workspaceId": str(false),
				}),
				Response: obj(map[string]PropSchema{
					"forwards": arrayOf("Forward", false),
				}),
			},
			{
				Name: "workspace.ports.add",
				Request: obj(map[string]PropSchema{
					"workspaceId": str(false),
					"spec":        ref("ExposeSpec", false),
				}),
				Response: obj(map[string]PropSchema{
					"forward": ref("Forward", false),
				}),
			},
			{
				Name: "workspace.ports.remove",
				Request: obj(map[string]PropSchema{
					"workspaceId": str(false),
					"forwardId":   str(false),
				}),
				Response: obj(map[string]PropSchema{
					"closed": boolean(false),
				}),
			},
			{
				Name: "workspace.discover-ports",
				Request: obj(map[string]PropSchema{
					"id": str(false),
				}),
				Response: obj(map[string]PropSchema{
					"ports": arrayOfType("int", false),
				}),
			},
			// ── pty ──────────────────────────────────────────────────────────────
			{
				Name: "pty.create",
				Request: obj(map[string]PropSchema{
					"workspaceId": str(false),
					"name":        str(false),
					"shell":       str(true),
					"args":        arrayOfType("string", true),
					"workDir":     str(true),
					"cols":        integer(false),
					"rows":        integer(false),
				}),
				Response: refType("PTYSessionInfo"),
			},
			{
				Name: "pty.list",
				Request: obj(map[string]PropSchema{
					"workspaceId": str(false),
				}),
				Response: obj(map[string]PropSchema{
					"sessions": arrayOf("PTYSessionInfo", false),
				}),
			},
			{
				Name: "pty.resize",
				Request: obj(map[string]PropSchema{
					"sessionId": str(false),
					"cols":      integer(false),
					"rows":      integer(false),
				}),
				Response: obj(map[string]PropSchema{
					"ok": boolean(false),
				}),
			},
			{
				Name: "pty.rename",
				Request: obj(map[string]PropSchema{
					"sessionId": str(false),
					"name":      str(false),
				}),
				Response: obj(map[string]PropSchema{
					"ok": boolean(false),
				}),
			},
			{
				Name: "pty.close",
				Request: obj(map[string]PropSchema{
					"sessionId": str(false),
				}),
				Response: obj(map[string]PropSchema{
					"ok": boolean(false),
				}),
			},
			{
				Name: "pty.write",
				Request: obj(map[string]PropSchema{
					"sessionId": str(false),
					"data":      str(false),
				}),
				Response: emptyObj(),
			},
			// ── spotlight ────────────────────────────────────────────────────────
			{
				Name: "spotlight.start",
				Request: obj(map[string]PropSchema{
					"workspaceId": str(false),
					"spec":        ref("ExposeSpec", false),
				}),
				Response: obj(map[string]PropSchema{
					"forward": ref("Forward", false),
				}),
			},
			{
				Name: "spotlight.list",
				Request: obj(map[string]PropSchema{
					"workspaceId": str(false),
				}),
				Response: obj(map[string]PropSchema{
					"forwards": arrayOf("Forward", false),
				}),
			},
			{
				Name: "spotlight.stop",
				Request: obj(map[string]PropSchema{
					"id": str(false),
				}),
				Response: obj(map[string]PropSchema{
					"closed": boolean(false),
				}),
			},
			// ── project ──────────────────────────────────────────────────────────
			{
				Name:    "project.list",
				Request: emptyObj(),
				Response: obj(map[string]PropSchema{
					"projects": arrayOf("Project", false),
				}),
			},
			{
				Name: "project.create",
				Request: obj(map[string]PropSchema{
					"name":    str(false),
					"repoUrl": str(false),
				}),
				Response: obj(map[string]PropSchema{
					"project": ref("Project", false),
				}),
			},
			{
				Name: "project.get",
				Request: obj(map[string]PropSchema{
					"id": str(false),
				}),
				Response: obj(map[string]PropSchema{
					"project": ref("Project", false),
				}),
			},
			{
				Name: "project.remove",
				Request: obj(map[string]PropSchema{
					"id": str(false),
				}),
				Response: obj(map[string]PropSchema{
					"removed": boolean(false),
				}),
			},
			// ── fs ───────────────────────────────────────────────────────────────
			{
				Name: "fs.readFile",
				Request: obj(map[string]PropSchema{
					"path":     str(false),
					"encoding": str(true),
				}),
				Response: obj(map[string]PropSchema{
					"content":  str(false),
					"encoding": str(false),
					"size":     integer(false),
				}),
			},
			{
				Name: "fs.writeFile",
				Request: obj(map[string]PropSchema{
					"path":     str(false),
					"content":  str(false),
					"encoding": str(true),
				}),
				Response: obj(map[string]PropSchema{
					"ok":   boolean(false),
					"path": str(false),
					"size": integer(false),
				}),
			},
			{
				Name: "fs.exists",
				Request: obj(map[string]PropSchema{
					"path": str(false),
				}),
				Response: obj(map[string]PropSchema{
					"exists": boolean(false),
					"path":   str(false),
				}),
			},
			{
				Name: "fs.readdir",
				Request: obj(map[string]PropSchema{
					"path": str(false),
				}),
				Response: obj(map[string]PropSchema{
					"entries": arrayOf("DirEntry", false),
					"path":    str(false),
				}),
			},
			{
				Name: "fs.mkdir",
				Request: obj(map[string]PropSchema{
					"path":      str(false),
					"recursive": boolean(true),
				}),
				Response: obj(map[string]PropSchema{
					"ok":   boolean(false),
					"path": str(false),
				}),
			},
			{
				Name: "fs.rm",
				Request: obj(map[string]PropSchema{
					"path":      str(false),
					"recursive": boolean(true),
				}),
				Response: obj(map[string]PropSchema{
					"ok":   boolean(false),
					"path": str(false),
				}),
			},
			{
				Name: "fs.stat",
				Request: obj(map[string]PropSchema{
					"path": str(false),
				}),
				Response: obj(map[string]PropSchema{
					"name":    str(false),
					"path":    str(false),
					"isDir":   boolean(false),
					"size":    integer(false),
					"mode":    str(false),
					"modTime": str(false),
				}),
			},
			// ── auth ─────────────────────────────────────────────────────────────
			{
				Name: "authrelay.mint",
				Request: obj(map[string]PropSchema{
					"workspaceId": str(false),
					"binding":     str(false),
					"ttlSeconds":  integer(true),
				}),
				Response: obj(map[string]PropSchema{
					"token": str(false),
				}),
			},
			{
				Name: "authrelay.revoke",
				Request: obj(map[string]PropSchema{
					"token": str(false),
				}),
				Response: obj(map[string]PropSchema{
					"revoked": boolean(false),
				}),
			},
			// ── node ─────────────────────────────────────────────────────────────
			{
				Name:    "node.info",
				Request: emptyObj(),
				Response: obj(map[string]PropSchema{
					"node":         ref("NodeInfo", false),
					"capabilities": arrayOf("Capability", false),
				}),
			},
		},
		Notifications: []Notification{
			{
				Name: "pty.data",
				Params: obj(map[string]PropSchema{
					"sessionId": str(false),
					"data":      str(false),
				}),
			},
			{
				Name: "pty.exit",
				Params: obj(map[string]PropSchema{
					"sessionId": str(false),
					"exitCode":  integer(false),
				}),
			},
		},
		Definitions: map[string]TypeSchema{
			"Workspace": obj(map[string]PropSchema{
				"id":                str(false),
				"projectId":         str(true),
				"repoId":            str(true),
				"repo":              str(false),
				"ref":               str(false),
				"workspaceName":     str(false),
				"agentProfile":      str(false),
				"policy":            ref("Policy", false),
				"state":             str(false),
				"rootPath":          str(false),
				"authBinding":       {Type: "object", Optional: true},
				"tunnelPorts":       arrayOfType("int", true),
				"parentWorkspaceId": str(true),
				"lineageRootId":     str(true),
				"backend":           str(true),
				"guestIp":           str(true),
				"configBundle":      str(true),
				"created_at":        timestamp(true),
				"updated_at":        timestamp(true),
			}),
			"CreateSpec": obj(map[string]PropSchema{
				"repo":          str(false),
				"ref":           str(false),
				"workspaceName": str(false),
				"agentProfile":  str(false),
				"policy":        ref("Policy", false),
				"backend":       str(true),
				"authBinding":   {Type: "object", Optional: true},
				"configBundle":  str(true),
				"projectId":     str(true),
			}),
			"Policy": obj(map[string]PropSchema{
				"autoStop":       boolean(true),
				"autoStopDelay":  integer(true),
				"isolationLevel": str(true),
				"maxLifetimeSec": integer(true),
			}),
			"PTYSessionInfo": obj(map[string]PropSchema{
				"id":          str(false),
				"workspaceId": str(false),
				"name":        str(false),
				"shell":       str(false),
				"workDir":     str(false),
				"cols":        integer(false),
				"rows":        integer(false),
				"createdAt":   timestamp(false),
				"isRemote":    boolean(false),
			}),
			"Forward": obj(map[string]PropSchema{
				"id":          str(false),
				"workspaceId": str(false),
				"localPort":   integer(false),
				"remotePort":  integer(false),
				"targetHost":  str(true),
				"protocol":    str(true),
				"state":       str(false),
				"created_at":  timestamp(true),
			}),
			"ExposeSpec": obj(map[string]PropSchema{
				"localPort":   integer(false),
				"remotePort":  integer(false),
				"protocol":    str(true),
				"source":      str(true),
				"workspaceId": str(false),
			}),
		"Project": obj(map[string]PropSchema{
				"id":        str(false),
				"name":      str(false),
				"repoUrl":   str(false),
				"createdAt": timestamp(true),
				"updatedAt": timestamp(true),
			}),
			"DirEntry": obj(map[string]PropSchema{
				"name":  str(false),
				"isDir": boolean(false),
				"size":  integer(false),
				"mode":  str(true),
			}),
			"NodeInfo": obj(map[string]PropSchema{
				"name": str(false),
				"tags": arrayOfType("string", true),
			}),
			"Capability": obj(map[string]PropSchema{
				"name":      str(false),
				"available": boolean(false),
			}),
		},
	}
}

func main() {
	var outFile string
	flag.StringVar(&outFile, "out", "", "write output to file instead of stdout")
	flag.Parse()

	schema := buildSchema()

	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal error: %v\n", err)
		os.Exit(1)
	}

	if outFile != "" {
		if err := os.WriteFile(outFile, data, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	fmt.Println(string(data))
}
