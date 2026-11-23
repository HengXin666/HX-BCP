package residential

import (
	cryptorand "crypto/rand"
	"fmt"
	"math/big"
	"strings"
)

// RegionMode controls how a residential allocation obtains its region.
type RegionMode string

const (
	RegionModeFixed             RegionMode = "fixed"
	RegionModeApplicationRandom RegionMode = "application-random"

	maximumRandomRegions = 64
)

// RegionSelection is the normalized region policy used for one allocation.
// RandomRegions is never sent as a whole to a vendor; one value is selected by
// the control plane immediately before the vendor request is made.
type RegionSelection struct {
	Mode          RegionMode
	Region        string
	RandomRegions []string
}

// SupportedRegionModes returns the stable values exposed by the admin API.
func SupportedRegionModes() []string {
	return []string{string(RegionModeFixed), string(RegionModeApplicationRandom)}
}

func normalizeRegionSelection(mode, region string, randomRegions []string) (RegionSelection, error) {
	selection := RegionSelection{
		Mode:   RegionMode(strings.ToLower(strings.TrimSpace(mode))),
		Region: strings.TrimSpace(region),
	}
	if selection.Mode == "" {
		selection.Mode = RegionModeFixed
	}
	if selection.Mode != RegionModeFixed && selection.Mode != RegionModeApplicationRandom {
		return RegionSelection{}, fmt.Errorf("%w: region_mode must be one of %s", ErrInvalid, strings.Join(SupportedRegionModes(), ", "))
	}
	if err := validateRegion(selection.Region); err != nil {
		return RegionSelection{}, err
	}
	normalizedRegions, err := normalizeRandomRegions(randomRegions)
	if err != nil {
		return RegionSelection{}, err
	}
	if selection.Mode == RegionModeFixed {
		if len(normalizedRegions) > 0 {
			return RegionSelection{}, fmt.Errorf("%w: random_regions requires region_mode %q", ErrInvalid, RegionModeApplicationRandom)
		}
		return selection, nil
	}
	if selection.Region != "" {
		return RegionSelection{}, fmt.Errorf("%w: region must be empty when region_mode is %q", ErrInvalid, RegionModeApplicationRandom)
	}
	if len(normalizedRegions) == 0 {
		return RegionSelection{}, fmt.Errorf("%w: application-random region mode requires at least one random region", ErrInvalid)
	}
	selection.RandomRegions = normalizedRegions
	return selection, nil
}

func normalizeRandomRegions(regions []string) ([]string, error) {
	if len(regions) > maximumRandomRegions {
		return nil, fmt.Errorf("%w: random_regions must contain at most %d regions", ErrInvalid, maximumRandomRegions)
	}
	if len(regions) == 0 {
		return nil, nil
	}
	result := make([]string, 0, len(regions))
	seen := make(map[string]struct{}, len(regions))
	for _, raw := range regions {
		region := strings.TrimSpace(raw)
		if err := validateRegion(region); err != nil {
			return nil, err
		}
		if region == "" {
			return nil, fmt.Errorf("%w: random_regions must not contain empty values", ErrInvalid)
		}
		if _, exists := seen[region]; exists {
			continue
		}
		seen[region] = struct{}{}
		result = append(result, region)
	}
	return result, nil
}

func chooseRegion(selection RegionSelection) (string, error) {
	if selection.Mode == RegionModeFixed {
		return selection.Region, nil
	}
	if len(selection.RandomRegions) == 0 {
		return "", fmt.Errorf("%w: application-random region selection has no candidates", ErrInvalid)
	}
	index, err := cryptorand.Int(cryptorand.Reader, big.NewInt(int64(len(selection.RandomRegions))))
	if err != nil {
		return "", fmt.Errorf("choose random residential region: %w", err)
	}
	return selection.RandomRegions[index.Int64()], nil
}

func providerRegionSelection(provider Provider) RegionSelection {
	mode := provider.DefaultRegionMode
	if mode == "" {
		mode = RegionModeFixed
	}
	return RegionSelection{
		Mode:          RegionMode(mode),
		Region:        provider.DefaultRegion,
		RandomRegions: append([]string(nil), provider.DefaultRandomRegions...),
	}
}

func validateRegion(region string) error {
	if region == "" {
		return nil
	}
	if len(region) > 64 {
		return fmt.Errorf("%w: region must contain at most 64 characters", ErrInvalid)
	}
	for _, character := range region {
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '-' && character != '_' {
			return fmt.Errorf("%w: region may only contain letters, digits, '-' and '_'", ErrInvalid)
		}
	}
	return nil
}
