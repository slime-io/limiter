package module

import (
	"os"

	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	limiterapiv1alpha1 "slime.io/slime/modules/limiter/api/v1alpha1"
	"slime.io/slime/modules/limiter/controllers"
	istioapi "slime.io/slime/slime-framework/apis"
	"slime.io/slime/slime-framework/bootstrap"
	istiocontroller "slime.io/slime/slime-framework/controllers"
	"slime.io/slime/slime-framework/model"
	"slime.io/slime/slime-framework/util"
)

const Name = "limiter"

type Module struct {
}

func (m *Module) Name() string {
	return Name
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
	rec := controllers.NewReconciler(mgr, &env)
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
