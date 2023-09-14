package v1alpha1

type LanguageCode string
type LocaleString string
type Locales map[LanguageCode]LocaleString

const (
	LanguageCodeEn      = "en"
	LanguageCodeZh      = "zh"
	DefaultLanguageCode = LanguageCodeEn
)

func NewLocales(enVal, zhVal string) Locales {
	return Locales{
		LanguageCodeEn: LocaleString(enVal),
		LanguageCodeZh: LocaleString(zhVal),
	}
}
