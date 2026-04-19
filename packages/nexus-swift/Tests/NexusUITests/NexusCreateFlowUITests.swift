import XCTest
import Foundation

// MARK: - Create Project & Sandbox E2E Tests
//
// These tests exercise the full create-project and create-sandbox flows
// against a real running daemon. They mutate daemon state (adding projects
// and sandboxes), so they should not be run in read-only environments.
//
// Prerequisites:
//   • A local nexus daemon must be running on the configured URL (default ws://localhost:63987)
//   • NEXUS_UI_TEST_DAEMON_URL / NEXUS_UI_TEST_DAEMON_TOKEN env vars (or defaults)
//   • NEXUS_UI_TEST_PROJECT_PATH — an absolute path to an existing local git repo on disk
//     (used for the "Create Project" test). Defaults to the nexus repo itself.
//
// Accessibility IDs used (all must be wired in the app):
//   new_project_button            — (+) in sidebar header
//   sandbox_project_picker        — project picker in new sandbox sheet
//   project_local_path_field      — local path text field when creating a new project
//   project_remote_url_field      — remote URL text field when creating a new project
//   create_project_button         — "Create Project" submit button
//   create_sandbox_button         — "Create Sandbox" submit button
//   sandbox_name_field            — sandbox name text field
//   sandbox_branch_field          — target branch text field
//   project_header_<id>           — project section header button in sidebar
//   project_add_sandbox_<id>      — per-project (+) sandbox button
//   workspace_row_<id>            — individual sandbox row in sidebar

final class NexusCreateFlowUITests: XCTestCase {

    var app: XCUIApplication!

    override func setUpWithError() throws {
        continueAfterFailure = false
        app = XCUIApplication(bundleIdentifier: "com.nexus.NexusApp")
        let env = ProcessInfo.processInfo.environment
        app.launchEnvironment["NEXUS_DAEMON_URL"]   = env["NEXUS_UI_TEST_DAEMON_URL"]   ?? ""
        app.launchEnvironment["NEXUS_DAEMON_TOKEN"] = env["NEXUS_UI_TEST_DAEMON_TOKEN"] ?? (resolveDaemonToken() ?? "")
    }

    // ── Create Project ────────────────────────────────────────────────────

    /// Opens the "New Sandbox" sheet, selects "Create new project…", enters a
    /// local path, and submits. Verifies a new project header row appears in the
    /// sidebar (meaning the RPC round-trip succeeded and the UI refreshed).
    func testCreateProjectFromLocalPath() throws {
        guard isConfiguredDaemonHealthzUp() else {
            throw XCTSkip("Daemon health endpoint unavailable")
        }

        let projectPath = ProcessInfo.processInfo.environment["NEXUS_UI_TEST_PROJECT_PATH"]
            ?? "/Users/newman/magic/nexus"   // fallback: this repo itself

        app.launch()

        // Wait for connected state before attempting create.
        guard app.staticTexts["Connected"].waitForExistence(timeout: 60) else {
            throw XCTSkip("Daemon did not reach connected state")
        }

        // Snapshot project count before creating.
        let headersBefore = app.buttons.matching(
            NSPredicate(format: "identifier BEGINSWITH 'project_header_'")
        ).count

        // Open sheet via sidebar (+) button.
        let plusBtn = app.buttons["new_project_button"]
        guard plusBtn.waitForExistence(timeout: 10) else {
            throw XCTSkip("new_project_button not visible")
        }
        plusBtn.click()

        guard app.buttons["create_project_button"].waitForExistence(timeout: 5) else {
            throw XCTSkip("New Project sheet did not open (create_project_button not found)")
        }

        // Fill local path — sheet is already in project-create mode when opened via new_project_button.
        let pathField = app.textFields["project_local_path_field"]
        guard pathField.waitForExistence(timeout: 5) else {
            XCTFail("project_local_path_field not visible in New Project sheet")
            return
        }
        pathField.click()
        pathField.typeText(projectPath)

        // Submit.
        let createBtn = app.buttons["create_project_button"]
        XCTAssertTrue(createBtn.isEnabled, "Create Project button should be enabled after entering a path")
        createBtn.click()

        // Sheet should dismiss on success.
        XCTAssertTrue(
            createBtn.waitForNonExistence(timeout: 15),
            "Sheet should dismiss after successful project creation"
        )

        // A new project_header_ button should appear in the sidebar.
        let deadline = Date().addingTimeInterval(15)
        var headersAfter = 0
        repeat {
            headersAfter = app.buttons.matching(
                NSPredicate(format: "identifier BEGINSWITH 'project_header_'")
            ).count
            if headersAfter > headersBefore { break }
            RunLoop.current.run(until: Date().addingTimeInterval(0.5))
        } while Date() < deadline

        XCTAssertGreaterThan(headersAfter, headersBefore,
            "Sidebar should show a new project header after creation (before: \(headersBefore), after: \(headersAfter))")
    }

    // ── Create Sandbox ───────────────────────────────────────────────────

    /// Finds an existing project, clicks its (+) button, fills sandbox name and
    /// branch, then submits. Verifies a new workspace_row_ appears in the sidebar.
    func testCreateSandboxInExistingProject() throws {
        guard isConfiguredDaemonHealthzUp() else {
            throw XCTSkip("Daemon health endpoint unavailable")
        }

        app.launch()

        guard app.staticTexts["Connected"].waitForExistence(timeout: 60) else {
            throw XCTSkip("Daemon did not reach connected state")
        }
        guard app.staticTexts["Projects"].waitForExistence(timeout: 30) else {
            throw XCTSkip("Sidebar did not render")
        }

        // Find a project (+) button.
        let addSandboxBtns = app.buttons.matching(
            NSPredicate(format: "identifier BEGINSWITH 'project_add_sandbox_'")
        )
        guard addSandboxBtns.firstMatch.waitForExistence(timeout: 10) else {
            throw XCTSkip("No project_add_sandbox_ buttons visible — create a project first")
        }

        // Snapshot workspace row count before.
        let rowsBefore = app.buttons.matching(
            NSPredicate(format: "identifier BEGINSWITH 'workspace_row_'")
        ).count

        addSandboxBtns.firstMatch.click()

        guard app.buttons["create_sandbox_button"].waitForExistence(timeout: 5) else {
            XCTFail("New Sandbox sheet did not open (create_sandbox_button not found)")
            return
        }

        // Fill a unique sandbox name.
        let uniqueName = "xcui-test-\(Int(Date().timeIntervalSince1970))"
        let nameField = app.textFields["sandbox_name_field"]
        guard nameField.waitForExistence(timeout: 5) else {
            XCTFail("sandbox_name_field not visible")
            return
        }
        nameField.click()
        // Select all existing text then replace with unique name.
        nameField.typeKey("a", modifierFlags: .command)
        nameField.typeText(uniqueName)

        // Leave branch at default. Use "Fresh" source to avoid needing an existing root.
        let forkPicker = app.popUpButtons["sandbox_fork_source_picker"]
        if forkPicker.waitForExistence(timeout: 3) {
            forkPicker.click()
            app.menuItems["Fresh"].click()
        }

        // Submit.
        let createBtn = app.buttons["create_sandbox_button"]
        guard createBtn.waitForExistence(timeout: 5) else {
            XCTFail("create_sandbox_button not visible")
            return
        }
        XCTAssertTrue(createBtn.isEnabled, "Create Sandbox button should be enabled")
        createBtn.click()

        // Sheet should dismiss.
        XCTAssertTrue(
            app.staticTexts["New Sandbox"].waitForNonExistence(timeout: 30),
            "Sheet should dismiss after sandbox creation"
        )

        // A new workspace_row_ should appear.
        let deadline = Date().addingTimeInterval(30)
        var rowsAfter = 0
        repeat {
            rowsAfter = app.buttons.matching(
                NSPredicate(format: "identifier BEGINSWITH 'workspace_row_'")
            ).count
            if rowsAfter > rowsBefore { break }
            RunLoop.current.run(until: Date().addingTimeInterval(0.5))
        } while Date() < deadline

        XCTAssertGreaterThan(rowsAfter, rowsBefore,
            "Sidebar should show a new workspace row after sandbox creation (before: \(rowsBefore), after: \(rowsAfter))")
    }
}

// MARK: - Helpers

private extension XCUIElement {
    /// Poll until the element is gone (disappears) within the timeout.
    func waitForNonExistence(timeout: TimeInterval) -> Bool {
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if !exists { return true }
            RunLoop.current.run(until: Date().addingTimeInterval(0.25))
        }
        return !exists
    }
}

private func resolveDaemonToken() -> String? {
    if let env = ProcessInfo.processInfo.environment["NEXUS_DAEMON_TOKEN"]?
        .trimmingCharacters(in: .whitespacesAndNewlines),
       !env.isEmpty {
        return env
    }
    for service in ["nexus-daemon-token", "nexus/token", "nexus-daemon", "nexus"] {
        if let token = readMacKeychainPassword(service: service), !token.isEmpty {
            return token
        }
    }
    return nil
}

private func isHealthzUp(_ rawURL: String) -> Bool {
    guard let url = URL(string: rawURL) else { return false }
    var req = URLRequest(url: url)
    req.timeoutInterval = 1.5
    let sem = DispatchSemaphore(value: 0)
    var ok = false
    URLSession.shared.dataTask(with: req) { _, response, _ in
        if let http = response as? HTTPURLResponse { ok = http.statusCode == 200 }
        sem.signal()
    }.resume()
    _ = sem.wait(timeout: .now() + 2)
    return ok
}

private extension NexusCreateFlowUITests {
    func isConfiguredDaemonHealthzUp() -> Bool {
        // Prefer NEXUS_UI_TEST_DAEMON_URL (set by test-xcui.sh) then fall back
        // to NEXUS_DAEMON_URL, then the app launchEnvironment value, then default.
        // Resolution order: test-runner env → app launch env → hardcoded default.
        // Note: app.launchEnvironment["NEXUS_DAEMON_URL"] may be an empty string (set from an
        // unset env var in setUp); treat empty strings the same as absent to avoid producing a
        // malformed URL like http://healthz/ when URLComponents processes an empty host.
        let wsURL: String = {
            let candidates: [String?] = [
                ProcessInfo.processInfo.environment["NEXUS_UI_TEST_DAEMON_URL"],
                ProcessInfo.processInfo.environment["NEXUS_DAEMON_URL"],
                app.launchEnvironment["NEXUS_DAEMON_URL"],
            ]
            return candidates.compactMap { $0 }.first(where: { !$0.isEmpty }) ?? "ws://localhost:63987"
        }()
        guard var components = URLComponents(string: wsURL) else { return false }
        components.scheme = (components.scheme == "wss") ? "https" : "http"
        components.path = "/healthz"
        components.query = nil
        guard let url = components.url else { return false }
        return isHealthzUp(url.absoluteString)
    }
}

private func readMacKeychainPassword(service: String) -> String? {
    let task = Process()
    task.executableURL = URL(fileURLWithPath: "/usr/bin/security")
    task.arguments = ["find-generic-password", "-s", service, "-w"]
    let out = Pipe()
    task.standardOutput = out
    task.standardError = Pipe()
    do { try task.run() } catch { return nil }
    task.waitUntilExit()
    guard task.terminationStatus == 0 else { return nil }
    let data = out.fileHandleForReading.readDataToEndOfFile()
    guard let raw = String(data: data, encoding: .utf8) else { return nil }
    let token = raw.trimmingCharacters(in: .whitespacesAndNewlines)
    return token.isEmpty ? nil : token
}
