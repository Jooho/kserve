"""
Tests for ValuesGenerator module
"""
import pytest
import yaml
from pathlib import Path
import tempfile

from helm_converter.values_generator import ValuesGenerator


class TestValuesGenerator:
    """Test ValuesGenerator functionality"""

    def test_build_inference_service_config_values(self, tmp_path):
        """Test building inferenceServiceConfig values section"""
        mapping = {
            'metadata': {
                'name': 'test-chart',
                'version': '1.0.0'
            },
            'inferenceServiceConfig': {
                'enabled': {
                    'valuePath': 'inferenceServiceConfig.enabled',
                    'defaultValue': True
                },
                'configMap': {
                    'manifestPath': 'config/configmap/inferenceservice.yaml',
                    'dataFields': [
                        {
                            'key': 'deploy',
                            'valuePath': 'inferenceServiceConfig.deploy',
                            'defaultValue': '{"defaultDeploymentMode": "Serverless"}'
                        }
                    ]
                }
            }
        }

        generator = ValuesGenerator(mapping, {}, tmp_path)
        values = generator._build_values()

        assert 'inferenceServiceConfig' in values
        assert values['inferenceServiceConfig']['enabled'] is True
        assert 'deploy' in values['inferenceServiceConfig']
        assert values['inferenceServiceConfig']['deploy']['defaultDeploymentMode'] == 'Serverless'

    def test_build_certmanager_values(self, tmp_path):
        """Test building certManager values"""
        mapping = {
            'metadata': {'name': 'test-chart'},
            'certManager': {
                'enabled': {
                    'valuePath': 'certManager.enabled',
                    'defaultValue': True
                }
            }
        }

        generator = ValuesGenerator(mapping, {}, tmp_path)
        values = generator._build_values()

        assert 'certManager' in values
        assert values['certManager']['enabled'] is True

    def test_build_component_values(self, tmp_path):
        """Test building component values"""
        mapping = {
            'metadata': {'name': 'kserve'},
            'kserve': {
                'enabled': {
                    'valuePath': 'kserve.enabled',
                    'defaultValue': True
                },
                'controllerManager': {
                    'image': {
                        'repository': {
                            'valuePath': 'kserve.controllerManager.image.repository',
                            'defaultValue': 'kserve/kserve-controller'
                        },
                        'tag': {
                            'valuePath': 'kserve.controllerManager.image.tag',
                            'defaultValue': 'latest'
                        }
                    },
                    'resources': {
                        'valuePath': 'kserve.controllerManager.resources',
                        'defaultValue': {
                            'limits': {'cpu': '100m', 'memory': '300Mi'},
                            'requests': {'cpu': '100m', 'memory': '200Mi'}
                        }
                    }
                }
            }
        }

        generator = ValuesGenerator(mapping, {}, tmp_path)
        values = generator._build_values()

        assert 'kserve' in values
        assert 'controllerManager' in values['kserve']
        assert values['kserve']['controllerManager']['image']['repository'] == 'kserve/kserve-controller'
        assert values['kserve']['controllerManager']['image']['tag'] == 'latest'
        assert values['kserve']['controllerManager']['resources']['limits']['cpu'] == '100m'

    def test_build_localmodel_values(self, tmp_path):
        """Test building localmodel values with enabled flag"""
        mapping = {
            'metadata': {'name': 'kserve'},
            'localmodel': {
                'enabled': {
                    'valuePath': 'localmodel.enabled',
                    'defaultValue': False
                },
                'controllerManager': {
                    'image': {
                        'repository': {
                            'valuePath': 'localmodel.controllerManager.image.repository',
                            'defaultValue': 'kserve/localmodel-controller'
                        }
                    }
                }
            }
        }

        generator = ValuesGenerator(mapping, {}, tmp_path)
        values = generator._build_values()

        assert 'localmodel' in values
        assert values['localmodel']['enabled'] is False
        assert values['localmodel']['controllerManager']['image']['repository'] == 'kserve/localmodel-controller'


if __name__ == '__main__':
    pytest.main([__file__, '-v'])
