import XCTest
@testable import NexusCore

final class MacAppFormalVerificationTests: XCTestCase {

    // Spec: MACAPP-001, MACAPP-PROOF-001
    func testDaemonInfoCompatibilityProtocolThreshold() {
        let compatible = DaemonInfo(name: "nexus", version: "0.2.0", commit: "", builtAt: "", protocolVersion: DaemonInfo.requiredProtocol)
        XCTAssertTrue(compatible.isCompatible)

        let incompatible = DaemonInfo(name: "nexus", version: "0.2.0", commit: "", builtAt: "", protocolVersion: DaemonInfo.requiredProtocol - 1)
        XCTAssertFalse(incompatible.isCompatible)
    }

    // Spec: MACAPP-001, MACAPP-PROOF-001
    func testDaemonInfoCompatibilityDevBuildException() {
        let devBuild = DaemonInfo(name: "nexus", version: "0.0.0-dev", commit: "", builtAt: "", protocolVersion: 0)
        XCTAssertTrue(devBuild.isCompatible)
    }

    // Spec: MACAPP-003, MACAPP-PROOF-002
    func testRemoteProvisionerIsExplicitlyDisabled() async {
        let profile = DaemonProfile(profileId: "p1", name: "Remote", port: 7777, isDefault: true, sshTarget: "dev@host")
        let provisioner = RemoteProvisioner(profile: profile)

        do {
            try await provisioner.provision()
            XCTFail("expected provisioningDisabled error")
        } catch let err as ProvisionError {
            guard case .provisioningDisabled = err else {
                XCTFail("expected provisioningDisabled, got: \(err)")
                return
            }
        } catch {
            XCTFail("unexpected error: \(error)")
        }
    }
}
