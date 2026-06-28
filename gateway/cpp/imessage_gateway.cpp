#include <Python.h>

#include <filesystem>
#include <iostream>
#include <sstream>
#include <string>
#include <vector>

namespace fs = std::filesystem;

namespace {

fs::path find_repo_root(const fs::path& start) {
    const char* env_root = std::getenv("IMESSAGE_MCP_ROOT");
    if (env_root && *env_root) {
        fs::path candidate(env_root);
        if (fs::exists(candidate / "gateway" / "imessage_client.py")) {
            return fs::weakly_canonical(candidate);
        }
    }

    fs::path current = start;
    for (int i = 0; i < 6 && !current.empty(); ++i) {
        if (fs::exists(current / "gateway" / "imessage_client.py")) {
            return fs::weakly_canonical(current);
        }
        current = current.parent_path();
    }
    return fs::weakly_canonical(start);
}

std::vector<std::string> collect_args(int argc, char* argv[]) {
    std::vector<std::string> args;
    args.reserve(static_cast<size_t>(argc > 1 ? argc - 1 : 0));
    for (int i = 1; i < argc; ++i) {
        args.emplace_back(argv[i]);
    }
    return args;
}

bool ensure_python_path(const fs::path& repo_root) {
    std::ostringstream command;
    command << "import sys\n"
            << "from pathlib import Path\n"
            << "repo = Path(r'" << repo_root.string() << "').resolve()\n"
            << "if str(repo) not in sys.path:\n"
            << "    sys.path.insert(0, str(repo))\n";
    return PyRun_SimpleString(command.str().c_str()) == 0;
}

int run_gateway(const std::vector<std::string>& args, const fs::path& repo_root) {
    if (!ensure_python_path(repo_root)) {
        std::cerr << "Failed to configure PYTHONPATH for repo root: " << repo_root << std::endl;
        return 1;
    }

    PyObject* module_name = PyUnicode_FromString("gateway.imessage_client");
    PyObject* module = PyImport_Import(module_name);
    Py_DECREF(module_name);

    if (!module) {
        PyErr_Print();
        std::cerr << "Unable to import gateway.imessage_client" << std::endl;
        return 1;
    }

    PyObject* execute_cli = PyObject_GetAttrString(module, "execute_cli");
    if (!execute_cli || !PyCallable_Check(execute_cli)) {
        Py_XDECREF(execute_cli);
        Py_DECREF(module);
        std::cerr << "gateway.imessage_client.execute_cli is not available" << std::endl;
        return 1;
    }

    PyObject* arg_list = PyList_New(static_cast<Py_ssize_t>(args.size()));
    for (Py_ssize_t i = 0; i < static_cast<Py_ssize_t>(args.size()); ++i) {
        PyObject* value = PyUnicode_FromString(args[static_cast<size_t>(i)].c_str());
        PyList_SET_ITEM(arg_list, i, value);  // Steals reference
    }

    PyObject* call_args = PyTuple_Pack(1, arg_list);
    Py_DECREF(arg_list);

    PyObject* result = PyObject_CallObject(execute_cli, call_args);
    Py_DECREF(call_args);
    Py_DECREF(execute_cli);
    Py_DECREF(module);

    if (!result) {
        PyErr_Print();
        std::cerr << "execute_cli raised an exception" << std::endl;
        return 1;
    }

    int return_code = 1;
    if (PyTuple_Check(result) && PyTuple_Size(result) == 3) {
        PyObject* code_obj = PyTuple_GetItem(result, 0);
        PyObject* stdout_obj = PyTuple_GetItem(result, 1);
        PyObject* stderr_obj = PyTuple_GetItem(result, 2);

        return_code = static_cast<int>(PyLong_AsLong(code_obj));

        if (PyUnicode_Check(stdout_obj)) {
            PyObject* bytes = PyUnicode_AsEncodedString(stdout_obj, "utf-8", "strict");
            if (bytes) {
                std::cout << PyBytes_AsString(bytes);
                Py_DECREF(bytes);
            }
        }

        if (PyUnicode_Check(stderr_obj)) {
            PyObject* bytes = PyUnicode_AsEncodedString(stderr_obj, "utf-8", "strict");
            if (bytes) {
                std::cerr << PyBytes_AsString(bytes);
                Py_DECREF(bytes);
            }
        }
    } else {
        std::cerr << "Unexpected return from execute_cli (expected tuple of length 3)" << std::endl;
    }

    Py_DECREF(result);
    return return_code;
}

}  // namespace

int main(int argc, char* argv[]) {
    fs::path start_path;
    try {
        start_path = fs::canonical(fs::path(argv[0])).parent_path();
    } catch (...) {
        start_path = fs::path(argv[0]).parent_path();
    }

    fs::path repo_root = find_repo_root(start_path);
    std::vector<std::string> args = collect_args(argc, argv);

    Py_Initialize();
    int return_code = run_gateway(args, repo_root);
    Py_Finalize();

    return return_code;
}
