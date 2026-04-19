import XCTest
import CoreGraphics

// MARK: - Nexus XCUITest Suite
//
// Tests run against an isolated nexusd on linuxbox:7778.
//
// Isolation: the daemon on linuxbox:7778 is separate from the dev daemon on
// linuxbox:7777. It is started by test-xcui.sh before running this suite and
// stopped after. No shared state, no cleanup needed between test runs.
//
// Prerequisites:
//   1. ssh newman@linuxbox is reachable (key in agent)
//   2. Isolated test daemon must be running on linuxbox:7778
//      (test-xcui.sh starts this)
//   3. Env vars injected by test-xcui.sh:
//        NEXUS_DAEMON_PORT  — remote port (7778)
//        (token is fetched at runtime via `nexus daemon token` SSH call)

final class NexusConnectedSmokeTest: XCTestCase {

    var app: XCUIApplication!

    override func setUpWithError() throws {
        continueAfterFailure = false
        app = XCUIApplication(bundleIdentifier: "com.nexus.NexusApp")
        let env = ProcessInfo.processInfo.environment

        // Pass all tunnel-mode vars to the app so AppState enters tunnel mode
        // and connects via SSH tunnel to the isolated test daemon on linuxbox:<port>.
        // NEXUS_DAEMON_URL triggers tunnel mode in AppState.
        // NEXUS_DAEMON_SSH_TARGET is the SSH host (newman@linuxbox).
        // NEXUS_DAEMON_PORT is the remote daemon port on linuxbox.
        // Token is fetched at runtime via `nexus daemon token` SSH call (no static override needed).
        app.launchEnvironment["NEXUS_DAEMON_URL"]        = env["NEXUS_DAEMON_URL"]        ?? "ws://127.0.0.1/dummy"
        app.launchEnvironment["NEXUS_DAEMON_SSH_TARGET"]  = env["NEXUS_DAEMON_SSH_TARGET"] ?? "newman@linuxbox"
        app.launchEnvironment["NEXUS_DAEMON_PORT"]       = env["NEXUS_DAEMON_PORT"]       ?? "7778"
    }

    override func tearDownWithError() throws {
        app.terminate()
    }

    // MARK: - Helpers

    private func attachScreenshot(named name: String) {
        let window = app.windows.firstMatch
        XCTAssertTrue(window.exists, "attachScreenshot: no app window found — window-only capture is required")
        guard window.exists else { return }
        let attachment = XCTAttachment(screenshot: window.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }

    /// Wait for the app to reach Connected state. Returns the button if label is
    /// "Connected"; otherwise returns the button and the caller can decide what to assert.
    @discardableResult
    private func waitForConnected(timeout: TimeInterval = 90) -> XCUIElement {
        let statusButton = app.buttons["connection_status"]
        let existed = statusButton.waitForExistence(timeout: timeout)
        if existed {
            // Budget 85s for SSH tunnel cold-start (SSH handshake + healthz poll
            // + token fetch + WebSocket connect + first RPC) before giving up.
            // The outer waitForExistence timeout is 90s, so use 85s to leave a
            // small margin and avoid the inner loop exiting before the outer one.
            let deadline = Date().addingTimeInterval(85)
            while Date() < deadline {
                let label = statusButton.label.lowercased()
                if label.contains("connected") {
                    return statusButton
                }
                // "Starting", "Connecting", or "Offline" — keep waiting
                RunLoop.current.run(until: Date().addingTimeInterval(0.5))
            }
        }
        return statusButton
    }

    // MARK: - Tests

    /// Smoke test: app launches, connects to isolated test daemon, screenshot attached.
    func testConnectsToIsolatedDaemonAndScreenshot() throws {
        app.launch()
        XCTAssertTrue(
            app.wait(for: .runningForeground, timeout: 20),
            "App should reach foreground"
        )

        let statusButton = waitForConnected()
        attachScreenshot(named: "01-after-connect")

        XCTAssertTrue(
            statusButton.exists,
            "connection_status button should appear"
        )
        XCTAssertTrue(
            statusButton.label.lowercased().contains("connected"),
            "Expected 'Connected', got: '\(statusButton.label)'"
        )
    }

    /// Real user flow: open the "New Project" sheet, fill fields, create a real project.
    func testCreateProjectSheetFlow() throws {
        app.launch()
        XCTAssertTrue(app.wait(for: .runningForeground, timeout: 20))
        waitForConnected()
        attachScreenshot(named: "02-connected-state")

        // Snapshot project header count before creating so we can assert a delta.
        let headersBefore = app.buttons.matching(
            NSPredicate(format: "identifier BEGINSWITH 'project_header_'")
        ).count

        // Step 1: Click the (+) new project button in the sidebar header
        let newProjectButton = app.buttons["new_project_button"]
        XCTAssertTrue(
            newProjectButton.waitForExistence(timeout: 10),
            "new_project_button should be visible after connecting"
        )
        newProjectButton.click()
        attachScreenshot(named: "03-new-project-sheet-opened")

        // Step 2: Verify the sheet opened in project mode (create_project_button visible,
        // title says "New Project", no sandbox button present).
        let createButton = app.buttons["create_project_button"]
        XCTAssertTrue(
            createButton.waitForExistence(timeout: 5),
            "Create Project button should appear in the sheet"
        )
        XCTAssertFalse(
            app.buttons["create_sandbox_button"].exists,
            "create_sandbox_button must NOT appear when intent is .newProject"
        )

        // Step 3: Fill in the local path field — use an existing git repo path so the
        // daemon accepts it (a non-existent path triggers 'repo is required' error).
        let localPathField = app.textFields["project_local_path_field"]
        XCTAssertTrue(
            localPathField.waitForExistence(timeout: 5),
            "Local path field should be present in New Project sheet"
        )

        // Use a real repo path that exists on the test machine (this repo itself).
        let repoPath = ProcessInfo.processInfo.environment["NEXUS_UI_TEST_PROJECT_PATH"]
            ?? "/Users/newman/magic/nexus"
        app.activate()
        localPathField.click()
        // Small settle after focus — avoids synthesize-event timeout caused by
        // external window (e.g. Ghostty) that interrupted the prior button click.
        Thread.sleep(forTimeInterval: 0.5)
        localPathField.typeText(repoPath)
        attachScreenshot(named: "04-sheet-fields-visible")

        // Step 4: Submit and wait for sheet to dismiss.
        createButton.click()

        // Sheet must dismiss — if it stays open an error occurred.
        let sheetDismissed = createButton.waitForNonExistence(timeout: 15)
        attachScreenshot(named: "05-project-created")

        // Fail if an error banner is visible (localError or appState.error text).
        let errorText = app.staticTexts.matching(
            NSPredicate(format: "label CONTAINS 'error' OR label CONTAINS 'Error' OR label CONTAINS 'rpc'")
        ).firstMatch
        XCTAssertFalse(errorText.exists, "Error banner must not be visible after creation: '\(errorText.exists ? errorText.label : "")'")

        XCTAssertTrue(sheetDismissed, "Sheet must dismiss after successful project creation — if it stays open, creation failed")

        // A new project_header_ button must appear (concrete creation evidence).
        let deadline = Date().addingTimeInterval(15)
        var headersAfter = 0
        repeat {
            headersAfter = app.buttons.matching(
                NSPredicate(format: "identifier BEGINSWITH 'project_header_'")
            ).count
            if headersAfter > headersBefore { break }
            RunLoop.current.run(until: Date().addingTimeInterval(0.5))
        } while Date() < deadline

        XCTAssertGreaterThan(
            headersAfter,
            headersBefore,
            "A new project_header_ row must appear in sidebar after creation (before: \(headersBefore), after: \(headersAfter))"
        )
    }

    /// Verify the sidebar renders with project rows and their controls.
    func testSidebarRendersAfterConnect() throws {
        app.launch()
        XCTAssertTrue(app.wait(for: .runningForeground, timeout: 20))
        waitForConnected()

        let sidebarAppeared = app.staticTexts["Projects"].waitForExistence(timeout: 15)
        attachScreenshot(named: "06-sidebar-state")

        XCTAssertTrue(sidebarAppeared, "Sidebar should show 'Projects' heading after connection")

        let newProjectButton = app.buttons["new_project_button"]
        XCTAssertTrue(
            newProjectButton.waitForExistence(timeout: 5),
            "new_project_button should exist in sidebar"
        )
    }

    /// Intent invariant: clicking new_project_button must open in project mode
    /// (create_project_button visible, create_sandbox_button absent).
    func testNewProjectIntentShowsProjectMode() throws {
        app.launch()
        XCTAssertTrue(app.wait(for: .runningForeground, timeout: 20))
        waitForConnected()

        let plusBtn = app.buttons["new_project_button"]
        XCTAssertTrue(plusBtn.waitForExistence(timeout: 10), "new_project_button must exist")
        plusBtn.click()

        let createProjectBtn = app.buttons["create_project_button"]
        XCTAssertTrue(
            createProjectBtn.waitForExistence(timeout: 5),
            "create_project_button must appear when intent is .newProject"
        )
        XCTAssertFalse(
            app.buttons["create_sandbox_button"].exists,
            "create_sandbox_button must NOT appear when intent is .newProject"
        )
        attachScreenshot(named: "07-project-intent-invariant")

        app.buttons.matching(NSPredicate(format: "identifier == 'cancel_button' OR label == 'Cancel'")).firstMatch.click()
    }

    /// Intent invariant: clicking a per-project (+) button must open in sandbox mode
    /// (create_sandbox_button visible, create_project_button absent).
    func testNewSandboxIntentShowsSandboxMode() throws {
        app.launch()
        XCTAssertTrue(app.wait(for: .runningForeground, timeout: 20))
        waitForConnected()

        let addSandboxBtns = app.buttons.matching(
            NSPredicate(format: "identifier BEGINSWITH 'project_add_sandbox_'")
        )
        guard addSandboxBtns.firstMatch.waitForExistence(timeout: 15) else {
            throw XCTSkip("No project_add_sandbox_ buttons — need at least one project")
        }
        addSandboxBtns.firstMatch.click()

        let createSandboxBtn = app.buttons["create_sandbox_button"]
        XCTAssertTrue(
            createSandboxBtn.waitForExistence(timeout: 5),
            "create_sandbox_button must appear when intent is .newSandbox(projectID)"
        )
        XCTAssertFalse(
            app.buttons["create_project_button"].exists,
            "create_project_button must NOT appear when intent is .newSandbox(projectID)"
        )
        attachScreenshot(named: "08-sandbox-intent-invariant")

        app.buttons.matching(NSPredicate(format: "identifier == 'cancel_button' OR label == 'Cancel'")).firstMatch.click()
    }
}

// MARK: - XCUIElement helpers

private extension XCUIElement {
    func waitForNonExistence(timeout: TimeInterval) -> Bool {
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if !exists { return true }
            RunLoop.current.run(until: Date().addingTimeInterval(0.25))
        }
        return !exists
    }
}

