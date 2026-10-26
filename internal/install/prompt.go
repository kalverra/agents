package install

import (
	"errors"
	"fmt"
	"os"
	"slices"

	"charm.land/huh/v2"
)

// ErrCancelled is returned when the user aborts a prompt.
var ErrCancelled = errors.New("install cancelled")

// HuhChooser is a Chooser that shows a terminal multi-select.
func HuhChooser(title string, options []Option, selected []string) ([]string, error) {
	opts := make([]huh.Option[string], len(options))
	for k, o := range options {
		opts[k] = huh.NewOption(o.Label, o.Value).Selected(slices.Contains(selected, o.Value))
	}
	var picked []string
	field := huh.NewMultiSelect[string]().Title(title).Options(opts...).Value(&picked)
	if err := runForm(field); err != nil {
		return nil, err
	}
	return picked, nil
}

// Confirm asks a yes/no question and returns ErrCancelled on no.
func Confirm(title string) error {
	var ok bool
	if err := runForm(huh.NewConfirm().Title(title).Affirmative("Yes").Negative("No").Value(&ok)); err != nil {
		return err
	}
	if !ok {
		return ErrCancelled
	}
	return nil
}

func runForm(field huh.Field) error {
	err := huh.NewForm(huh.NewGroup(field)).
		WithTheme(huh.ThemeFunc(huh.ThemeCharm)).
		WithAccessible(os.Getenv("ACCESSIBLE") != "").
		Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return ErrCancelled
	}
	if err != nil {
		return fmt.Errorf("prompt: %w", err)
	}
	return nil
}
