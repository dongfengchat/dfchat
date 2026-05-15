import { Navigate, Route, Routes } from 'react-router-dom';
import Login from './pages/Login';
import Register from './pages/Register';
import Home from './pages/Home';
import Admin from './pages/Admin';
import Settings from './pages/Settings';
import Live from './pages/Live';
import { useUserStore } from './store/userStore';
import { Toaster } from './components/ui/Toast';
import UpdateBanner from './components/UpdateBanner';

export default function App() {
  const token = useUserStore((s) => s.accessToken);
  return (
    <>
      <Routes>
        <Route path="/" element={<Navigate to={token ? '/home' : '/login'} replace />} />
        <Route path="/login" element={<Login />} />
        <Route path="/register" element={<Register />} />
        <Route path="/home" element={<Home />} />
        <Route path="/admin" element={<Admin />} />
        <Route path="/settings" element={<Settings />} />
        <Route path="/live" element={<Live />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
      <Toaster />
      <UpdateBanner />
    </>
  );
}
