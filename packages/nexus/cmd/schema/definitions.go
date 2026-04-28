package main

func buildDefinitions() (map[string]TypeSchema, []Notification) {
	definitions := map[string]TypeSchema{
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
	}

	notifications := []Notification{
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
	}

	return definitions, notifications
}
