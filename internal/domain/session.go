package domain

type Session struct {
	SessionName string
	Phone       string

	// настройки приложения/устройства
	DeviceModel        string
	SystemVersion      string
	ApplicationVersion string
	LangCode           string

	// можно добавить флаги "активен/забанен" и т.п.
}
