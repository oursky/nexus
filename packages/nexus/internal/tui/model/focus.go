package model

import (
	tea "github.com/charmbracelet/bubbletea"
)

// Focusable represents an element that can receive focus.
type Focusable interface {
	Focus() tea.Cmd
	Blur() tea.Cmd
	Focused() bool
}

// FocusManager manages focus between multiple elements.
type FocusManager struct {
	elements []Focusable
	active   int
}

// NewFocusManager creates a new FocusManager.
func NewFocusManager(elements ...Focusable) *FocusManager {
	return &FocusManager{
		elements: elements,
		active:   0,
	}
}

// Next moves focus to the next element.
func (f *FocusManager) Next() tea.Cmd {
	if len(f.elements) == 0 {
		return nil
	}

	// Blur current
	blurCmd := f.elements[f.active].Blur()

	// Advance
	f.active = (f.active + 1) % len(f.elements)

	// Focus next
	focusCmd := f.elements[f.active].Focus()

	return tea.Batch(blurCmd, focusCmd)
}

// Prev moves focus to the previous element.
func (f *FocusManager) Prev() tea.Cmd {
	if len(f.elements) == 0 {
		return nil
	}

	// Blur current
	blurCmd := f.elements[f.active].Blur()

	// Retreat
	f.active = (f.active - 1 + len(f.elements)) % len(f.elements)

	// Focus prev
	focusCmd := f.elements[f.active].Focus()

	return tea.Batch(blurCmd, focusCmd)
}

// Active returns the currently focused element.
func (f *FocusManager) Active() Focusable {
	if len(f.elements) == 0 {
		return nil
	}
	return f.elements[f.active]
}
