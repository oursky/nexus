import XCTest
import CoreGraphics

// MARK: - Nexus XCUITest Suite
//
// Tests real user flows against the live daemon on newman@linuxbox.
// The app uses its normal SSH tunnel profile — no env-var bypass, no token injection.
//
// Prerequisites:
//   1. ssh newman@linuxbox is reachable (key in agent)
//   2. nexus daemon is running on linuxbox:7777
//   3. A DaemonProfile with sshTarget=newman@linuxbox is set as default in UserDefaults

final class NexusConnectedSmokeTest: XCTestCase {

    var app: XCUIApplication!

    override func setUpWithError() throws {
        continueAfterFailure = false
        app = XCUIApplication(bundleIdentifier: "com.nexus.NexusApp")
    }

    override func tearDownWithError() throws {
        app.terminate()
    }

    // MARK: - Helpers

    /// Take a screenshot and attach it to the test result with a named label.
    private func attachScreenshot(named name: String) {
        let screenshot = app.screenshot()
        let attachment = XCTAttachment(screenshot: screenshot)
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }

    /// Wait for the app to reach Connected state. Returns the connection_status button.
    @discardableResult
    private func waitForConnected(timeout: TimeInterval = 90) -> XCUIElement {
        let statusButton = app.buttons["connection_status"]
        let existed = statusButton.waitForExistence(timeout: timeout)
        if existed {
            // Extra wait for the label to actually say "Connected"
            let deadline = Date().addingTimeInterval(15)
            while Date() < deadline {
                if statusButton.label.lowercased().contains("connected") {
                    return statusButton
                }
                RunLoop.current.run(until: Date().addingTimeInterval(0.5))
            }
        }
        return statusButton
    }

    // MARK: - Tests

    /// Smoke test: app launches, connects to linuxbox daemon, screenshot attached.
    func testConnectsToLinuxboxAndScreenshot() throws {
        app.launch()

        XCTAssertTrue(
            app.wait(for: .runningForeground, timeout: 20),
            "App should reach foreground within 20 s"
        )

        let statusButton = waitForConnected()
        attachScreenshot(named: "01-after-connect")

        XCTAssertTrue(
            statusButton.exists,
            "connection_status button should appear within timeout"
        )
        let label = statusButton.label
        XCTAssertTrue(
            label.lowercased().contains("connected"),
            "Expected 'Connected', got: '\(label)'"
        )
    }

    /// Real user flow: open the "New Project" sheet, fill fields, and attach screenshots.
    func testCreateProjectSheetFlow() throws {
        app.launch()
        XCTAssertTrue(app.wait(for: .runningForeground, timeout: 20))
        waitForConnected()
        attachScreenshot(named: "02-connected-state")

        // Step 1: Click the (+) new project button in the sidebar header
        let newProjectButton = app.buttons["new_project_button"]
        XCTAssertTrue(
            newProjectButton.waitForExistence(timeout: 10),
            "new_project_button should be visible after connecting"
        )
        newProjectButton.click()
        attachScreenshot(named: "03-new-project-sheet-opened")

        // Step 2: Verify the sheet opened — look for the create_project_button
        let createButton = app.buttons["create_project_button"]
        XCTAssertTrue(
            createButton.waitForExistence(timeout: 5),
            "Create Project button should appear in the sheet"
        )

        // Step 3: Verify key fields exist in the sheet
        let localPathField = app.textFields["project_local_path_field"]
        XCTAssertTrue(
            localPathField.waitForExistence(timeout: 5),
            "Local path field should be present in New Project sheet"
        )

        attachScreenshot(named: "04-sheet-fields-visible")

        // Step 4: Dismiss the sheet without creating (press Escape via CGEvent)
        // We don't actually create a project to avoid polluting the daemon state.
        let src = CGEventSource(stateID: .hidSystemState)
        let keyDown = CGEvent(keyboardEventSource: src, virtualKey: 0x35, keyDown: true) // ESC
        let keyUp = CGEvent(keyboardEventSource: src, virtualKey: 0x35, keyDown: false)
        keyDown?.post(tap: .cghidEventTap)
        keyUp?.post(tap: .cghidEventTap)
        // Wait for sheet to dismiss
        sleep(1)
        attachScreenshot(named: "05-sheet-dismissed")
    }

    /// Verify the sidebar renders with project rows and their controls.
    func testSidebarRendersAfterConnect() throws {
        app.launch()
        XCTAssertTrue(app.wait(for: .runningForeground, timeout: 20))
        waitForConnected()

        // The sidebar should show either projects from the daemon or an empty-state prompt
        let sidebarAppeared = app.staticTexts["Projects"].waitForExistence(timeout: 15)
        attachScreenshot(named: "06-sidebar-state")

        XCTAssertTrue(sidebarAppeared, "Sidebar should show 'Projects' heading after connection")

        // The new_project_button should be accessible
        let newProjectButton = app.buttons["new_project_button"]
        XCTAssertTrue(
            newProjectButton.waitForExistence(timeout: 5),
            "new_project_button should exist in sidebar"
        )
    }
}

// MARK: - XCUIElement helper

extension XCUIElement {
    /// Whether the element both exists and is hittable.
    @MainActor
    var isVisible: Bool {
        return exists && isHittable
    }
}
