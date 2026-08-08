(function () {
    'use strict';

    angular.module('ariaNg').factory('transferGatewayService', ['$http', '$q', '$window', 'ariaNgSettingService', function ($http, $q, $window, ariaNgSettingService) {
        var config = $window.ariaNgTransferGateway || {};

        var getBaseUrl = function () {
            if (config.url) {
                return config.url.replace(/\/$/, '');
            }

            return '/gateway';
        };

        var getHeaders = function () {
            var headers = {
                'Content-Type': 'application/json'
            };
            var token = config.token || ariaNgSettingService.getCurrentRpcSecret();
            if (token) {
                headers.Authorization = 'Bearer ' + token;
            }
            return headers;
        };

        var request = function (method, path, data, params) {
            return $http({
                method: method,
                url: getBaseUrl() + path,
                headers: getHeaders(),
                data: data,
                params: params
            }).then(function (response) {
                return response.data;
            });
        };

        var toAriaNgResponse = function (task) {
            return {
                success: true,
                hasSuccess: true,
                data: task.gid,
                taskId: task.id,
                task: task
            };
        };

        var createTask = function (type, content, urls, options, pause, destinationId, targetPath) {
            return request('POST', '/api/v1/tasks', {
                type: type,
                content: content || '',
                urls: urls || [],
                options: options || {},
                pause: !!pause,
                destination_id: destinationId,
                target_path: targetPath
            }).then(toAriaNgResponse);
        };

        return {
            isEnabled: function () {
                return config.enabled !== false;
            },
            getDestinations: function () {
                return request('GET', '/api/v1/destinations');
            },
            createDestination: function (destination) {
                return request('POST', '/api/v1/destinations', destination);
            },
            updateDestination: function (id, destination) {
                return request('PUT', '/api/v1/destinations/' + encodeURIComponent(id), destination);
            },
            deleteDestination: function (id) {
                return request('DELETE', '/api/v1/destinations/' + encodeURIComponent(id));
            },
            setDefaultDestination: function (id) {
                return request('PUT', '/api/v1/destinations/' + encodeURIComponent(id) + '/default');
            },
            createUriTasks: function (tasks, pause, destinationId, targetPath) {
                var requests = [];
                for (var i = 0; i < tasks.length; i++) {
                    requests.push(createTask(
                        'urls',
                        '',
                        tasks[i].urls,
                        tasks[i].options,
                        pause,
                        destinationId,
                        targetPath
                    ));
                }
                return $q.all(requests).then(function (results) {
                    return {
                        success: true,
                        hasSuccess: results.length > 0,
                        results: results
                    };
                });
            },
            createContentTask: function (type, content, options, pause, destinationId, targetPath) {
                return createTask(type, content, [], options, pause, destinationId, targetPath);
            },
            getTasks: function (filters) {
                filters = filters || {};
                var params = {};
                if (filters.status) {
                    params.status = filters.status;
                }
                if (filters.destinationId) {
                    params.destination_id = filters.destinationId;
                }
                if (filters.query) {
                    params.q = filters.query;
                }
                return request('GET', '/api/v1/tasks', null, params);
            },
            getTaskByGid: function (gid) {
                return request('GET', '/api/v1/tasks/by-gid/' + encodeURIComponent(gid));
            },
            retryTask: function (id, mode) {
                return request('POST', '/api/v1/tasks/' + encodeURIComponent(id) + '/retry', null, {
                    mode: mode || 'upload'
                });
            },
            retryTasks: function (ids, mode) {
                return request('POST', '/api/v1/tasks/retry', {
                    ids: ids || [],
                    mode: mode || 'upload'
                });
            },
            deleteTask: function (id) {
                return request('DELETE', '/api/v1/tasks/' + encodeURIComponent(id));
            },
            deleteTasks: function (ids) {
                return request('POST', '/api/v1/tasks/delete', {
                    ids: ids || []
                });
            },
            deleteTasksByGids: function (gids) {
                return request('POST', '/api/v1/tasks/delete-by-gid', {
                    gids: gids || []
                });
            }
        };
    }]);
}());
