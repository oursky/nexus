package main

func buildMethods() []Method {
	return []Method{
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
	}
}
