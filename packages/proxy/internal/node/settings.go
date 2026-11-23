package node

import (
	"context"

	"github.com/HengXin666/hx-pxy/internal/systemsettings"
)

// ErrInvalidSettings marks quality-settings validation failures so the API
// layer can map them onto a 422 response.
var ErrInvalidSettings = systemsettings.ErrInvalid

// QualitySettings controls the periodic node quality checks. All values are
// administrator-editable at runtime; zero values fall back to the defaults
// below when loading.
type QualitySettings = systemsettings.QualitySettings
type HealthTarget = systemsettings.HealthTarget

func DefaultQualitySettings() QualitySettings {
	return systemsettings.Default().Quality
}

// QualitySettings returns the stored settings, falling back to defaults when
// nothing has been saved yet or the stored payload cannot be decoded.
func (s *Service) QualitySettings(ctx context.Context) (QualitySettings, error) {
	settings, err := systemsettings.Load(ctx, s.repository)
	if err != nil {
		return QualitySettings{}, err
	}
	return settings.Quality, nil
}

func (s *Service) UpdateQualitySettings(ctx context.Context, settings QualitySettings) (QualitySettings, error) {
	global, err := systemsettings.Load(ctx, s.repository)
	if err != nil {
		return QualitySettings{}, err
	}
	if settings.ProbeConcurrency == 0 {
		settings.ProbeConcurrency = global.Quality.ProbeConcurrency
	}
	if settings.HealthTargets == nil {
		settings.HealthTargets = global.Quality.HealthTargets
	}
	global.Quality = settings
	if err := systemsettings.Save(ctx, s.repository, global); err != nil {
		return QualitySettings{}, err
	}
	saved, err := systemsettings.Load(ctx, s.repository)
	if err != nil {
		return QualitySettings{}, err
	}
	return saved.Quality, nil
}
