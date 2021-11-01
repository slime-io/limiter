package module

import (
	"os"
	"slime.io/slime/framework/model/module"
	"slime.io/slime/modules/limiter/model"

	"github.com/golang/protobuf/proto"
	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	istioapi "slime.io/slime/framework/apis"
	"slime.io/slime/framework/apis/config/v1alpha1"
	"slime.io/slime/framework/bootstrap"
	istiocontroller "slime.io/slime/framework/controllers"
	"slime.io/slime/framework/util"
	limiterapiv1alpha2 "slime.io/slime/modules/limiter/api/v1alpha2"
	"slime.io/slime/modules/limiter/controllers"
)

type Module struct {
	config v1alpha1.Limiter
}

func (m *Module) Name() string {
	return model.ModuleName
}

func (m *Module) Config() proto.Message {
	return &m.config
}

func (m *Module) InitScheme(scheme *runtime.Scheme) error {
	for _, f := range []func(*runtime.Scheme) error{
		clientgoscheme.AddToScheme,
		limiterapiv1alpha2.AddToScheme,
		istioapi.AddToScheme,
	} {
		if err := f(scheme); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) InitManager(mgr manager.Manager, env bootstrap.Environment, cbs module.InitCallbacks) error {
	cfg := &m.config
	if env.Config != nil && env.Config.Limiter != nil {
		cfg = env.Config.Limiter
	}

	rec := controllers.NewReconciler(cfg, mgr, &env)
	if err := rec.SetupWithManager(mgr); err != nil {
		log.Errorf("unable to create controller SmartLimiter, %+v", err)
		util.Fatal()
		return nil
	}

	// add dr reconcile
	if err := (&istiocontroller.DestinationRuleReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		log.Errorf("unable to create controller DestinationRule, %+v", err)
		os.Exit(1)
	}

	return nil
}
