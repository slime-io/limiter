package module

import (
	"os"

	"github.com/golang/protobuf/proto"
	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	limiterapiv1alpha1 "slime.io/slime/modules/limiter/api/v1alpha1"
	"slime.io/slime/modules/limiter/controllers"
	istioapi "slime.io/slime/slime-framework/apis"
	"slime.io/slime/slime-framework/apis/config/v1alpha1"
	"slime.io/slime/slime-framework/bootstrap"
	istiocontroller "slime.io/slime/slime-framework/controllers"
	"slime.io/slime/slime-framework/model"
	"slime.io/slime/slime-framework/util"
)

const Name = "limiter"

type Module struct {
	config v1alpha1.Limiter
}

func (m *Module) Name() string {
	return Name
}

func (m *Module) Config() proto.Message {
	return &m.config
}

func (m *Module) InitScheme(scheme *runtime.Scheme) error {
	for _, f := range []func(*runtime.Scheme) error{
		clientgoscheme.AddToScheme,
		limiterapiv1alpha1.AddToScheme,
		istioapi.AddToScheme,
	} {
		if err := f(scheme); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) InitManager(mgr manager.Manager, env bootstrap.Environment, cbs model.ModuleInitCallbacks) error {
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
