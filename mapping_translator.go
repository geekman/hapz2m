package hapz2m

import (
	"fmt"
	"reflect"
)

// Implements a translator between the exposed property and Characteristic.
// Generally translators should be flexible to translate in either direction,
// e.g. a percentage to 0-255 translator should be able to apply the percentage
// to either the exposed property side, or the Characteristic side, but the
// MappingTranslator is a fixed direction for simplicity.
type MappingTranslator interface {
	ToCharacteristicValue(exposedValue any) (cValue any, err error)
	ToExposedValue(cValue any) (exposedValue any, err error)
}

// Default pass-through "translator", where both exposed and Characteristic
// values are of the same or similar types.
type PassthruTranslator struct{}

var defaultTranslator = &PassthruTranslator{}

func (p *PassthruTranslator) ToExposedValue(v any) (any, error)        { return v, nil }
func (p *PassthruTranslator) ToCharacteristicValue(v any) (any, error) { return v, nil }

// Chains another Translator to transform values further.
// You can chain another Translator on the ExposedSide or the CharacteristicSide:
//
//	Exposed   -- ToCharacteristicValue() -->  Characteristic
//	 Value       <--- ToExposedValue() ---        Value
type ChainedTranslator struct{ ExposedSide, CharacteristicSide MappingTranslator }

func (t *ChainedTranslator) ToExposedValue(cVal any) (any, error) {
	v, err := t.CharacteristicSide.ToExposedValue(cVal)
	if err != nil {
		return v, err
	}
	return t.ExposedSide.ToExposedValue(v)
}

func (t *ChainedTranslator) ToCharacteristicValue(eVal any) (any, error) {
	v, err := t.ExposedSide.ToCharacteristicValue(eVal)
	if err != nil {
		return v, err
	}
	return t.CharacteristicSide.ToCharacteristicValue(v)
}

// Wraps a Translator and flips the translation direction.
// This allows a translator to work for either an exposed value or
// Characteristic value.
type FlippedTranslator struct{ T MappingTranslator }

func (t *FlippedTranslator) ToExposedValue(cVal any) (any, error) {
	return t.T.ToCharacteristicValue(cVal)
}

func (t *FlippedTranslator) ToCharacteristicValue(eVal any) (any, error) {
	return t.T.ToExposedValue(eVal)
}

var ErrTranslationError = fmt.Errorf("cannot translate value")

// Translates a binary type exposed value to specified Characteristic T/F values
type BoolTranslator struct{ TrueValue, FalseValue any }

func (t *BoolTranslator) ToExposedValue(cVal any) (any, error) {
	switch cVal {
	case t.TrueValue:
		return true, nil

	case t.FalseValue:
		return false, nil
	}
	return nil, ErrTranslationError
}

func (t *BoolTranslator) ToCharacteristicValue(eVal any) (any, error) {
	bVal, ok := eVal.(bool)
	if !ok {
		return nil, ErrTranslationError
	} else if bVal {
		return t.TrueValue, nil
	}
	return t.FalseValue, nil
}

// Translates a numeric type exposed value to percentage Characteristic values
type PercentageTranslator struct{ Min, Max float64 }

func (t *PercentageTranslator) ToExposedValue(cVal any) (any, error) {
	cVal2, ok := valToFloat64(cVal)
	if !ok {
		return nil, ErrTranslationError
	}
	v := t.Min + (cVal2 / 100. * (t.Max - t.Min))
	return v, nil
}

func (t *PercentageTranslator) ToCharacteristicValue(eVal any) (any, error) {
	eVal2, ok := valToFloat64(eVal)
	if !ok {
		return nil, ErrTranslationError
	}
	v := (eVal2 - t.Min) * 100. / (t.Max - t.Min)
	return v, nil
}

// Converts numeric values to float64, if possible
// Returns the converted float64 value and a bool indicating if it was successful.
func valToFloat64(v any) (float64, bool) {
	val := reflect.ValueOf(v)
	switch {
	case val.CanInt():
		return float64(val.Int()), true
	case val.CanUint():
		return float64(val.Uint()), true
	case val.CanFloat():
		return val.Float(), true
	}
	return 0, false
}

// Translates an string enum type exposed value to specified Characteristic values
type EnumTranslator struct{ EnumMap map[string]any }

func (t *EnumTranslator) ToExposedValue(cVal any) (any, error) {
	for k, v := range t.EnumMap {
		if v == cVal {
			return k, nil
		}
	}
	return nil, ErrTranslationError
}

func (t *EnumTranslator) ToCharacteristicValue(eVal any) (any, error) {
	if sVal, ok := eVal.(string); ok {
		if v, ok := t.EnumMap[sVal]; ok {
			return v, nil
		}
	}
	return nil, ErrTranslationError
}
