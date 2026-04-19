# Lima VM for NexusUITests CI

This Lima VM config boots an Ubuntu 24.04 Linux guest that hosts the Nexus daemon. Port 7777 on the guest is forwarded to `127.0.0.1:7777` on the macOS host, so XCUITests running on the host can reach the daemon without any extra networking setup.

## Local usage

Start the VM:

```sh
limactl start scripts/lima/xcui-vm.yaml
```

Shell into the VM:

```sh
limactl shell xcui-vm
```

## How CI uses it

1. `limactl start scripts/lima/xcui-vm.yaml` — boot the VM (provisions Docker and Go on first run)
2. Build the Nexus daemon inside the VM: `limactl shell xcui-vm -- bash -c 'cd ~/nexus && go build -o nexusd ./packages/nexus/cmd/nexusd'`
3. Start the daemon inside the VM: `limactl shell xcui-vm -- ~/nexus/nexusd`
4. Run XCUITests from the macOS host with `xcodebuild test` — the tests connect to `127.0.0.1:7777` which Lima transparently forwards into the VM
