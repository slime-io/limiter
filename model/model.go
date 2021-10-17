package model

import (
	"github.com/sirupsen/logrus"
	frameworkmodel "slime.io/slime/framework/model"
)

const ModuleName = "limiter"

var ModuleLog = logrus.WithField(frameworkmodel.LogFieldKeyModule, ModuleName)
