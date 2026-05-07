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
    // Provisioning is enabled but auto-provision on app start is disabled.
    // Users must manually trigger provisioning via Settings.
    func testRemoteProvisionerRequiresValidSSHTarget() async {
        // Profile with no SSH target should fail with noSSHTarget
        let profileNoTarget = DaemonProfile(profileId: "p1", name: "Remote", port: 7777, isDefault: true, sshTarget: nil)
        let provisionerNoTarget = RemoteProvisioner(profile: profileNoTarget)

        do {
            try await provisionerNoTarget.provision()
            XCTFail("expected noSSHTarget error")
        } catch let err as ProvisionError {
            guard case .noSSHTarget = err else {
                XCTFail("expected noSSHTarget, got: \(err)")
                return
            }
        } catch {
            XCTFail("unexpected error: \(error)")
        }
    }
}
