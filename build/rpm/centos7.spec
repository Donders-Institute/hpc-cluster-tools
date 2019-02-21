# build with the following command:
# rpmbuild -bb
%define debug_package %{nil}

Name:       hpc-cluster-tools
Version:    %{getenv:VERSION}
Release:    1%{?dist}
Summary:    Unified CLI tools for cluster utilities and services.
License:    FIXME
URL: https://github.com/Donders-Institute/%{name}
Source0: https://github.com/Donders-Institute/%{name}/archive/%{version}.tar.gz

BuildArch: x86_64
BuildRequires: systemd

# defin the GOPATH that is created later within the extracted source code.
%define gopath %{_tmppath}/go.rpmbuild-%{name}-%{version}

%description
Unified CLI tools for cluster utilities and services.

%prep
%setup -q

%build
# create GOPATH structure within the source code
rm -rf %{gopath}
mkdir -p %{gopath}/src/github.com/Donders-Institute
# copy entire directory into gopath, this duplicate the source code
cp -R %{_builddir}/%{name}-%{version} %{gopath}/src/github.com/Donders-Institute/%{name}
cd %{gopath}/src/github.com/Donders-Institute/%{name}; GOPATH=%{gopath} make; %{gopath}/bin/cluster init

%install
mkdir -p %{buildroot}/%{_bindir}
## install files for client tools
install -m 755 %{gopath}/bin/cluster %{buildroot}/%{_bindir}/cluster
install -m 755 %{gopath}/src/github.com/Donders-Institute/%{name}/cluster %{buildroot}/%{_sysconfdir}/bach_completion.d/cluster

%files
%{_bindir}/cluster
%{_sysconfdir}/bach_completion.d/cluster

%clean
rm -rf %{gopath} 
rm -f %{_topdir}/SOURCES/%{version}.tar.gz
rm -rf $RPM_BUILD_ROOT

%changelog
* Thu Feb 21 2019 Hong Lee <h.lee@donders.ru.nl> - 0.1
- first rpmbuild implementation
